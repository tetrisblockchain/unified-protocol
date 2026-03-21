package consensus

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"math/bits"
	"math/rand"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gocolly/colly/v2"

	"unified/core/constants"
	coregov "unified/core/governance"
)

const (
	SimHashSimilarityThreshold = 0.95
	ValidatorSampleSize        = 3
	ValidatorQuorum            = 2
	DefaultCrawlerUserAgent    = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"
)

var (
	ErrEmptyQuery             = errors.New("consensus: crawl query is required")
	ErrInsufficientValidators = errors.New("consensus: at least three validators are required")
	ErrInvalidBounty          = errors.New("consensus: base bounty must be non-negative")
	ErrInvalidPriorityRule    = errors.New("consensus: priority rule is invalid")
	ErrInvalidURL             = errors.New("consensus: crawl target must be an absolute URL")
	ErrNilCrawler             = errors.New("consensus: crawler is required")
	ErrNilGovernanceSource    = errors.New("consensus: governance event source is required")
	ErrNilPriorityRegistry    = errors.New("consensus: priority registry is required")
	ErrNoIndexableResult      = errors.New("consensus: miner could not index any candidate URL")
	ErrNoSeedURLs             = errors.New("consensus: at least one candidate URL is required")
)

// CrawlTask packages a user query into work that miners can execute against seed URLs.
type CrawlTask struct {
	ID                      string
	Query                   string
	SeedURLs                []string
	BaseDifficulty          uint64
	Difficulty              uint64
	AdjustedDifficulty      uint64
	DataVolumeBytes         uint64
	BaseBounty              *big.Int
	TotalBounty             *big.Int
	ArchitectFee            *big.Int
	MinerReward             *big.Int
	GovernanceMultiplierBPS uint64
	PrioritySectors         []string
	CreatedAt               time.Time
	ExpiresAt               time.Time
}

// IndexedPage is the crawl ledger payload produced by a successful PoUW run.
type IndexedPage struct {
	URL           string
	Title         string
	Body          string
	Snippet       string
	OutboundLinks []string
	StatusCode    int
	BodyBytes     uint64
	ContentHash   string
	QueryCoverage float64
	SimHash       uint64
	IndexedAt     time.Time
}

type Crawler interface {
	Index(ctx context.Context, task CrawlTask, targetURL string) (IndexedPage, error)
}

type CollyCrawler struct {
	UserAgent      string
	AllowedDomains []string
	RequestTimeout time.Duration
	MaxBodyBytes   int
}

type PriorityRegistry struct {
	mu    sync.RWMutex
	rules map[string]coregov.PriorityRule
}

type GovernanceListener struct {
	Source    coregov.EventSource
	Registry  *PriorityRegistry
	FromBlock uint64
}

type TaskAdjustment struct {
	URL                string
	MultiplierBPS      uint64
	AdjustedBounty     *big.Int
	ArchitectFee       *big.Int
	NetMinerReward     *big.Int
	AdjustedDifficulty uint64
	PrioritySectors    []string
	MatchedRules       []coregov.PriorityRule
}

type Miner struct {
	ID               string
	Crawler          Crawler
	PriorityRegistry *PriorityRegistry
}

type MiningResult struct {
	TaskID                 string
	MinerID                string
	URL                    string
	Page                   IndexedPage
	ProofHash              string
	AppliedMultiplierBPS   uint64
	AppliedPrioritySectors []string
	AdjustedDifficulty     uint64
	AdjustedBounty         *big.Int
	ArchitectFee           *big.Int
	MinerReward            *big.Int
	CompletedAt            time.Time
}

type ValidatorNode interface {
	ID() string
	Index(ctx context.Context, task CrawlTask, targetURL string) (IndexedPage, error)
	ResolveGovernance(task CrawlTask, targetURL string) (CrawlTask, TaskAdjustment, error)
}

type Validator struct {
	NodeID           string
	Crawler          Crawler
	PriorityRegistry *PriorityRegistry
}

type ValidatorObservation struct {
	ValidatorID           string
	Similarity            float64
	Matched               bool
	GovernanceMatched     bool
	ExpectedMultiplierBPS uint64
	ExpectedDifficulty    uint64
	ExpectedBounty        string
	ExpectedArchitectFee  string
	ExpectedMinerReward   string
	PageHash              string
	SimHash               uint64
	Error                 string
}

type ValidationResult struct {
	Approved            bool
	GovernanceValidated bool
	AverageSimilarity   float64
	MatchingValidators  int
	Observations        []ValidatorObservation
	SampleSize          int
	Threshold           float64
}

func NewPriorityRegistry() *PriorityRegistry {
	return &PriorityRegistry{
		rules: make(map[string]coregov.PriorityRule),
	}
}

func NewCrawlTask(query string, seedURLs []string, baseBounty *big.Int, difficulty, dataVolume uint64, ttl time.Duration) (CrawlTask, error) {
	normalizedQuery := strings.TrimSpace(query)
	if normalizedQuery == "" {
		return CrawlTask{}, ErrEmptyQuery
	}

	normalizedURLs, err := normalizeSeedURLs(seedURLs)
	if err != nil {
		return CrawlTask{}, err
	}

	quotedBounty, err := QuoteBounty(baseBounty, difficulty, dataVolume)
	if err != nil {
		return CrawlTask{}, err
	}

	createdAt := time.Now().UTC()
	task := CrawlTask{
		Query:                   normalizedQuery,
		SeedURLs:                normalizedURLs,
		BaseDifficulty:          difficulty,
		Difficulty:              difficulty,
		AdjustedDifficulty:      difficulty,
		DataVolumeBytes:         dataVolume,
		BaseBounty:              cloneBigInt(baseBounty),
		TotalBounty:             quotedBounty,
		ArchitectFee:            constants.ArchitectFee(quotedBounty),
		MinerReward:             new(big.Int).Sub(cloneBigInt(quotedBounty), constants.ArchitectFee(quotedBounty)),
		GovernanceMultiplierBPS: constants.DefaultMultiplierBPS,
		CreatedAt:               createdAt,
	}

	if ttl > 0 {
		task.ExpiresAt = createdAt.Add(ttl)
	}

	task.ID = buildTaskID(task)
	return task, nil
}

func QuoteBounty(baseBounty *big.Int, difficulty, dataVolume uint64) (*big.Int, error) {
	base := cloneBigInt(baseBounty)
	if base.Sign() < 0 {
		return nil, ErrInvalidBounty
	}

	extra := new(big.Int).Mul(
		new(big.Int).SetUint64(difficulty),
		new(big.Int).SetUint64(dataVolume),
	)

	return new(big.Int).Add(base, extra), nil
}

func (c CollyCrawler) Index(ctx context.Context, task CrawlTask, targetURL string) (IndexedPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if err := ctx.Err(); err != nil {
		return IndexedPage{}, err
	}

	target, err := normalizeURL(targetURL)
	if err != nil {
		return IndexedPage{}, err
	}

	result := IndexedPage{
		URL:       target,
		IndexedAt: time.Now().UTC(),
	}

	collector := colly.NewCollector(colly.AllowURLRevisit())
	if strings.TrimSpace(c.UserAgent) != "" {
		collector.UserAgent = c.UserAgent
	} else {
		collector.UserAgent = DefaultCrawlerUserAgent
	}
	if len(c.AllowedDomains) > 0 {
		collector.AllowedDomains = append([]string(nil), c.AllowedDomains...)
	}
	if c.MaxBodyBytes > 0 {
		collector.MaxBodySize = c.MaxBodyBytes
	}
	collector.ParseHTTPErrorResponse = true
	if c.RequestTimeout > 0 {
		collector.SetRequestTimeout(c.RequestTimeout)
	}
	collector.WithTransport(&http.Transport{
		DisableKeepAlives:     true,
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: c.RequestTimeout,
	})

	var crawlErr error
	links := make([]string, 0, 16)

	collector.OnResponse(func(response *colly.Response) {
		result.StatusCode = response.StatusCode
		result.BodyBytes = uint64(len(response.Body))

		sum := sha256.Sum256(response.Body)
		result.ContentHash = hex.EncodeToString(sum[:])
		if response.StatusCode >= http.StatusBadRequest && crawlErr == nil {
			crawlErr = errors.New("consensus: crawl returned HTTP " + strconv.Itoa(response.StatusCode))
		}
	})

	collector.OnHTML("title", func(element *colly.HTMLElement) {
		if result.Title == "" {
			result.Title = collapseWhitespace(element.Text)
		}
	})

	collector.OnHTML("body", func(element *colly.HTMLElement) {
		if result.Body == "" {
			result.Body = collapseWhitespace(element.Text)
		}
	})

	collector.OnHTML("a[href]", func(element *colly.HTMLElement) {
		href := element.Request.AbsoluteURL(element.Attr("href"))
		if href != "" {
			links = append(links, href)
		}
	})

	collector.OnError(func(_ *colly.Response, err error) {
		crawlErr = err
	})

	if err := collector.Visit(target); err != nil {
		return IndexedPage{}, err
	}
	if crawlErr != nil {
		return IndexedPage{}, crawlErr
	}
	if err := ctx.Err(); err != nil {
		return IndexedPage{}, err
	}

	if result.Title == "" && result.Body == "" {
		return IndexedPage{}, ErrNoIndexableResult
	}

	result.OutboundLinks = uniqueStrings(links)
	result.Snippet = truncateRunes(result.Body, 280)
	if result.Snippet == "" {
		result.Snippet = truncateRunes(result.Title, 280)
	}
	result.QueryCoverage = QueryCoverage(task.Query, result.Title+" "+result.Body)
	result.SimHash = SimHash(result.Title + " " + result.Body)

	return result, nil
}

func (l *GovernanceListener) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if l.Source == nil {
		return ErrNilGovernanceSource
	}
	if l.Registry == nil {
		return ErrNilPriorityRegistry
	}

	events, errs, err := l.Source.SubscribeGovernanceEvents(ctx, l.FromBlock)
	if err != nil {
		return err
	}

	for events != nil || errs != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				return err
			}
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}

			rule := event.ToPriorityRule()
			if err := l.Registry.UpsertRule(rule); err != nil {
				return err
			}
			if event.BlockNumber >= l.FromBlock {
				l.FromBlock = event.BlockNumber + 1
			}
		}
	}

	return nil
}

func (r *PriorityRegistry) Snapshot() []coregov.PriorityRule {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	rules := make([]coregov.PriorityRule, 0, len(r.rules))
	for _, rule := range r.rules {
		rules = append(rules, rule)
	}

	sort.Slice(rules, func(i, j int) bool {
		if rules[i].Sector == rules[j].Sector {
			return rules[i].SourceProposalID < rules[j].SourceProposalID
		}
		return rules[i].Sector < rules[j].Sector
	})

	return rules
}

func (r *PriorityRegistry) UpsertRule(rule coregov.PriorityRule) error {
	if r == nil {
		return ErrNilPriorityRegistry
	}

	normalizedSector := strings.TrimSpace(strings.ToLower(rule.Sector))
	if normalizedSector == "" || rule.MultiplierBPS == 0 {
		return ErrInvalidPriorityRule
	}

	updated := rule
	updated.Sector = normalizedSector
	if updated.ActivatedAt.IsZero() {
		updated.ActivatedAt = time.Now().UTC()
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rules == nil {
		r.rules = make(map[string]coregov.PriorityRule)
	}
	r.rules[normalizedSector] = updated
	return nil
}

func (r *PriorityRegistry) Apply(task CrawlTask, targetURL string) (TaskAdjustment, error) {
	normalizedTarget, err := normalizeURL(targetURL)
	if err != nil {
		return TaskAdjustment{}, err
	}

	baseBounty, err := quoteBaseTaskBounty(task)
	if err != nil {
		return TaskAdjustment{}, err
	}

	adjustment := TaskAdjustment{
		URL:                normalizedTarget,
		MultiplierBPS:      constants.DefaultMultiplierBPS,
		AdjustedBounty:     baseBounty,
		ArchitectFee:       constants.ArchitectFee(baseBounty),
		NetMinerReward:     new(big.Int).Sub(cloneBigInt(baseBounty), constants.ArchitectFee(baseBounty)),
		AdjustedDifficulty: task.baseDifficulty(),
	}

	if r == nil {
		return adjustment, nil
	}

	rules := r.Snapshot()
	if len(rules) == 0 {
		return adjustment, nil
	}

	composite := constants.DefaultMultiplierBPS
	matchedRules := make([]coregov.PriorityRule, 0, len(rules))
	for _, rule := range rules {
		if !priorityRuleMatchesURL(normalizedTarget, rule.Sector) {
			continue
		}

		matchedRules = append(matchedRules, rule)
		composite = constants.ComposeBasisPoints(composite, rule.MultiplierBPS)
	}

	if len(matchedRules) == 0 {
		return adjustment, nil
	}

	sectors := make([]string, 0, len(matchedRules))
	for _, rule := range matchedRules {
		sectors = append(sectors, rule.Sector)
	}

	adjustment.MultiplierBPS = composite
	adjustment.AdjustedDifficulty = constants.ScaleUint64Ceil(task.baseDifficulty(), composite)
	adjustment.AdjustedBounty = constants.ApplyBasisPoints(baseBounty, composite)
	adjustment.ArchitectFee = constants.ArchitectFee(adjustment.AdjustedBounty)
	adjustment.NetMinerReward = new(big.Int).Sub(cloneBigInt(adjustment.AdjustedBounty), constants.ArchitectFee(adjustment.AdjustedBounty))
	adjustment.PrioritySectors = uniqueStrings(sectors)
	adjustment.MatchedRules = matchedRules
	return adjustment, nil
}

func (m Miner) Mine(ctx context.Context, task CrawlTask) (MiningResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if m.Crawler == nil {
		return MiningResult{}, ErrNilCrawler
	}
	if len(task.SeedURLs) == 0 {
		return MiningResult{}, ErrNoSeedURLs
	}

	var lastErr error
	for _, targetURL := range task.SeedURLs {
		result, err := m.MineURL(ctx, task, targetURL)
		if err == nil {
			return result, nil
		}
		lastErr = err
	}

	if lastErr == nil {
		lastErr = ErrNoIndexableResult
	}

	return MiningResult{}, lastErr
}

func (m Miner) MineURL(ctx context.Context, task CrawlTask, targetURL string) (MiningResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if m.Crawler == nil {
		return MiningResult{}, ErrNilCrawler
	}

	governedTask, adjustment, err := applyGovernanceToTask(task, targetURL, m.PriorityRegistry)
	if err != nil {
		return MiningResult{}, err
	}

	page, err := m.Crawler.Index(ctx, governedTask, governedTask.SeedURLs[0])
	if err != nil {
		return MiningResult{}, err
	}

	completedAt := time.Now().UTC()
	return MiningResult{
		TaskID:                 governedTask.ID,
		MinerID:                m.ID,
		URL:                    page.URL,
		Page:                   page,
		ProofHash:              buildProofHash(governedTask.ID, page.URL, page.ContentHash, page.SimHash),
		AppliedMultiplierBPS:   adjustment.MultiplierBPS,
		AppliedPrioritySectors: append([]string(nil), adjustment.PrioritySectors...),
		AdjustedDifficulty:     adjustment.AdjustedDifficulty,
		AdjustedBounty:         cloneBigInt(adjustment.AdjustedBounty),
		ArchitectFee:           cloneBigInt(adjustment.ArchitectFee),
		MinerReward:            cloneBigInt(adjustment.NetMinerReward),
		CompletedAt:            completedAt,
	}, nil
}

func (v Validator) ID() string {
	return v.NodeID
}

func (v Validator) ResolveGovernance(task CrawlTask, targetURL string) (CrawlTask, TaskAdjustment, error) {
	return applyGovernanceToTask(task, targetURL, v.PriorityRegistry)
}

func (v Validator) Index(ctx context.Context, task CrawlTask, targetURL string) (IndexedPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if v.Crawler == nil {
		return IndexedPage{}, ErrNilCrawler
	}

	return v.Crawler.Index(ctx, task, targetURL)
}

// VerifyMiningResult samples three validators and accepts the block when at least
// two independently reproduce the miner payload above the 95% similarity threshold
// while agreeing on the active governance multiplier for the crawled URL.
func VerifyMiningResult(ctx context.Context, task CrawlTask, mined MiningResult, validators []ValidatorNode, rng *rand.Rand) (ValidationResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	sampled, err := sampleValidators(validators, ValidatorSampleSize, rng, mined.MinerID)
	if err != nil {
		return ValidationResult{}, err
	}

	observations := make([]ValidatorObservation, len(sampled))
	var wg sync.WaitGroup

	for i, validator := range sampled {
		wg.Add(1)
		go func(index int, validator ValidatorNode) {
			defer wg.Done()

			observation := ValidatorObservation{ValidatorID: validator.ID()}
			governedTask, adjustment, err := validator.ResolveGovernance(task, mined.URL)
			if err != nil {
				observation.Error = err.Error()
				observations[index] = observation
				return
			}

			observation.ExpectedMultiplierBPS = adjustment.MultiplierBPS
			observation.ExpectedDifficulty = adjustment.AdjustedDifficulty
			observation.ExpectedBounty = cloneBigInt(adjustment.AdjustedBounty).String()
			observation.ExpectedArchitectFee = cloneBigInt(adjustment.ArchitectFee).String()
			observation.ExpectedMinerReward = cloneBigInt(adjustment.NetMinerReward).String()
			observation.GovernanceMatched = governanceMatches(mined, adjustment)
			if !observation.GovernanceMatched {
				observation.Error = "consensus: governance multiplier mismatch"
				observations[index] = observation
				return
			}

			page, err := validator.Index(ctx, governedTask, mined.URL)
			if err != nil {
				observation.Error = err.Error()
				observations[index] = observation
				return
			}

			observation.PageHash = page.ContentHash
			observation.SimHash = page.SimHash
			observation.Similarity = Similarity(mined.Page.SimHash, page.SimHash)
			observation.Matched = observation.Similarity > SimHashSimilarityThreshold
			observations[index] = observation
		}(i, validator)
	}

	wg.Wait()

	matching := 0
	governanceMatchesCount := 0
	totalSimilarity := 0.0
	successes := 0
	for _, observation := range observations {
		if observation.GovernanceMatched {
			governanceMatchesCount++
		}
		if observation.Error != "" {
			continue
		}

		successes++
		totalSimilarity += observation.Similarity
		if observation.Matched {
			matching++
		}
	}

	average := 0.0
	if successes > 0 {
		average = totalSimilarity / float64(successes)
	}

	governanceValidated := governanceMatchesCount >= ValidatorQuorum
	return ValidationResult{
		Approved:            matching >= ValidatorQuorum && governanceValidated,
		GovernanceValidated: governanceValidated,
		AverageSimilarity:   average,
		MatchingValidators:  matching,
		Observations:        observations,
		SampleSize:          len(sampled),
		Threshold:           SimHashSimilarityThreshold,
	}, nil
}

func QueryCoverage(query, text string) float64 {
	queryTokens := uniqueStrings(tokenize(query))
	if len(queryTokens) == 0 {
		return 0
	}

	corpus := make(map[string]struct{}, len(queryTokens))
	for _, token := range tokenize(text) {
		corpus[token] = struct{}{}
	}

	matches := 0
	for _, token := range queryTokens {
		if _, ok := corpus[token]; ok {
			matches++
		}
	}

	return float64(matches) / float64(len(queryTokens))
}

func Similarity(left, right uint64) float64 {
	return 1 - float64(HammingDistance(left, right))/64.0
}

func HammingDistance(left, right uint64) int {
	return bits.OnesCount64(left ^ right)
}

func SimHash(text string) uint64 {
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return 0
	}

	weights := [64]int{}
	frequencies := make(map[string]int, len(tokens))
	for _, token := range tokens {
		frequencies[token]++
	}

	for token, weight := range frequencies {
		sum := sha256.Sum256([]byte(token))
		fingerprint := binary.BigEndian.Uint64(sum[:8])

		for bit := 0; bit < len(weights); bit++ {
			if fingerprint&(1<<uint(bit)) != 0 {
				weights[bit] += weight
				continue
			}

			weights[bit] -= weight
		}
	}

	var fingerprint uint64
	for bit, weight := range weights {
		if weight > 0 {
			fingerprint |= 1 << uint(bit)
		}
	}

	return fingerprint
}

func buildProofHash(taskID, targetURL, contentHash string, simHash uint64) string {
	payload, _ := json.Marshal(struct {
		TaskID      string `json:"taskId"`
		URL         string `json:"url"`
		ContentHash string `json:"contentHash"`
		SimHash     uint64 `json:"simHash"`
	}{
		TaskID:      taskID,
		URL:         targetURL,
		ContentHash: contentHash,
		SimHash:     simHash,
	})

	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func buildTaskID(task CrawlTask) string {
	baseBounty, err := quoteBaseTaskBounty(task)
	if err != nil {
		baseBounty = big.NewInt(0)
	}

	payload := strings.Join([]string{
		task.Query,
		strings.Join(task.SeedURLs, ","),
		strconv.FormatUint(task.baseDifficulty(), 10),
		strconv.FormatUint(task.DataVolumeBytes, 10),
		baseBounty.String(),
		strconv.FormatInt(task.CreatedAt.UnixNano(), 10),
	}, "|")

	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:16])
}

func applyGovernanceToTask(task CrawlTask, targetURL string, registry *PriorityRegistry) (CrawlTask, TaskAdjustment, error) {
	normalizedTarget, err := normalizeURL(targetURL)
	if err != nil {
		return CrawlTask{}, TaskAdjustment{}, err
	}

	adjustment, err := registry.Apply(task, normalizedTarget)
	if err != nil {
		return CrawlTask{}, TaskAdjustment{}, err
	}

	governed := task
	governed.SeedURLs = []string{normalizedTarget}
	governed.Difficulty = adjustment.AdjustedDifficulty
	governed.AdjustedDifficulty = adjustment.AdjustedDifficulty
	governed.GovernanceMultiplierBPS = adjustment.MultiplierBPS
	governed.TotalBounty = cloneBigInt(adjustment.AdjustedBounty)
	governed.ArchitectFee = cloneBigInt(adjustment.ArchitectFee)
	governed.MinerReward = cloneBigInt(adjustment.NetMinerReward)
	governed.PrioritySectors = append([]string(nil), adjustment.PrioritySectors...)
	return governed, adjustment, nil
}

func governanceMatches(mined MiningResult, adjustment TaskAdjustment) bool {
	if mined.AppliedMultiplierBPS != adjustment.MultiplierBPS {
		return false
	}
	if mined.AdjustedDifficulty != adjustment.AdjustedDifficulty {
		return false
	}
	if cloneBigInt(mined.AdjustedBounty).Cmp(cloneBigInt(adjustment.AdjustedBounty)) != 0 {
		return false
	}
	if cloneBigInt(mined.ArchitectFee).Cmp(cloneBigInt(adjustment.ArchitectFee)) != 0 {
		return false
	}
	if cloneBigInt(mined.MinerReward).Cmp(cloneBigInt(adjustment.NetMinerReward)) != 0 {
		return false
	}

	return sameStringSet(mined.AppliedPrioritySectors, adjustment.PrioritySectors)
}

func quoteBaseTaskBounty(task CrawlTask) (*big.Int, error) {
	return QuoteBounty(task.BaseBounty, task.baseDifficulty(), task.DataVolumeBytes)
}

func (t CrawlTask) baseDifficulty() uint64 {
	if t.BaseDifficulty != 0 {
		return t.BaseDifficulty
	}

	return t.Difficulty
}

func cloneBigInt(value *big.Int) *big.Int {
	if value == nil {
		return big.NewInt(0)
	}

	return new(big.Int).Set(value)
}

func collapseWhitespace(input string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
}

func normalizeSeedURLs(seedURLs []string) ([]string, error) {
	if len(seedURLs) == 0 {
		return nil, ErrNoSeedURLs
	}

	normalized := make([]string, 0, len(seedURLs))
	seen := make(map[string]struct{}, len(seedURLs))
	for _, candidate := range seedURLs {
		target, err := normalizeURL(candidate)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		normalized = append(normalized, target)
	}

	if len(normalized) == 0 {
		return nil, ErrNoSeedURLs
	}

	return normalized, nil
}

func normalizeURL(raw string) (string, error) {
	target, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || target.Scheme == "" || target.Host == "" {
		return "", ErrInvalidURL
	}

	target.Fragment = ""
	target.Scheme = strings.ToLower(target.Scheme)
	target.Host = strings.ToLower(target.Host)
	return target.String(), nil
}

func sampleValidators(validators []ValidatorNode, sampleSize int, rng *rand.Rand, excludeID string) ([]ValidatorNode, error) {
	pool := make([]ValidatorNode, 0, len(validators))
	for _, validator := range validators {
		if validator == nil {
			continue
		}
		if excludeID != "" && validator.ID() == excludeID {
			continue
		}

		pool = append(pool, validator)
	}

	if len(pool) < sampleSize {
		return nil, ErrInsufficientValidators
	}

	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}

	rng.Shuffle(len(pool), func(i, j int) {
		pool[i], pool[j] = pool[j], pool[i]
	})

	return append([]ValidatorNode(nil), pool[:sampleSize]...), nil
}

func tokenize(input string) []string {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, input)

	return strings.Fields(normalized)
}

func truncateRunes(input string, limit int) string {
	if limit <= 0 {
		return ""
	}

	runes := []rune(input)
	if len(runes) <= limit {
		return input
	}

	return strings.TrimSpace(string(runes[:limit]))
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		cleaned := strings.TrimSpace(value)
		if cleaned == "" {
			continue
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}

		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}

	sort.Strings(out)
	return out
}

func priorityRuleMatchesURL(rawURL, sector string) bool {
	normalizedSector := strings.TrimSpace(strings.ToLower(sector))
	if normalizedSector == "" {
		return false
	}

	target, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	host := strings.ToLower(target.Hostname())
	path := strings.ToLower(target.EscapedPath())
	full := strings.ToLower(rawURL)

	if strings.HasPrefix(normalizedSector, ".") {
		suffix := strings.TrimPrefix(normalizedSector, ".")
		return host == suffix || strings.HasSuffix(host, "."+suffix)
	}
	if strings.HasPrefix(normalizedSector, "host:") {
		expected := strings.TrimPrefix(normalizedSector, "host:")
		return host == expected || strings.HasSuffix(host, "."+expected)
	}
	if strings.HasPrefix(normalizedSector, "path:") {
		expected := strings.TrimPrefix(normalizedSector, "path:")
		return strings.Contains(path, expected)
	}

	return strings.Contains(host, normalizedSector) || strings.Contains(path, normalizedSector) || strings.Contains(full, normalizedSector)
}

func sameStringSet(left, right []string) bool {
	a := uniqueStrings(left)
	b := uniqueStrings(right)
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}
