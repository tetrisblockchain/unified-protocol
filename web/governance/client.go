package governanceclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	coregov "unified/core/governance"
)

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *Client) ActiveProposals(ctx context.Context) ([]coregov.ProposalSummary, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/governance/proposals?status=active", nil)
	if err != nil {
		return nil, err
	}

	response, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("governance client: proposal fetch failed with HTTP %d", response.StatusCode)
	}

	var proposals []coregov.ProposalSummary
	if err := json.NewDecoder(response.Body).Decode(&proposals); err != nil {
		return nil, err
	}

	return proposals, nil
}

func (c *Client) CastVote(ctx context.Context, proposalID uint64, choice coregov.VoteChoice) error {
	if ctx == nil {
		ctx = context.Background()
	}

	payload, err := json.Marshal(struct {
		ProposalID uint64 `json:"proposalId"`
		Choice     uint8  `json:"choice"`
	}{
		ProposalID: proposalID,
		Choice:     choice.Uint8(),
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.BaseURL+"/governance/vote",
		bytes.NewReader(payload),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("governance client: vote failed with HTTP %d", response.StatusCode)
	}

	return nil
}

func (c *Client) SubscribeGovernanceEvents(ctx context.Context, fromBlock uint64) (<-chan coregov.GovernanceEvent, <-chan error, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	events := make(chan coregov.GovernanceEvent, 32)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		nextBlock := fromBlock
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			batch, err := c.fetchGovernanceEvents(ctx, nextBlock)
			if err != nil {
				select {
				case errs <- err:
				case <-ctx.Done():
				}
				return
			}

			for _, event := range batch {
				if event.BlockNumber >= nextBlock {
					nextBlock = event.BlockNumber + 1
				}

				select {
				case events <- event:
				case <-ctx.Done():
					return
				}
			}

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	return events, errs, nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}

	return &http.Client{Timeout: 15 * time.Second}
}

func (c *Client) fetchGovernanceEvents(ctx context.Context, fromBlock uint64) ([]coregov.GovernanceEvent, error) {
	query := url.Values{}
	query.Set("fromBlock", strconv.FormatUint(fromBlock, 10))

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		c.BaseURL+"/governance/events?"+query.Encode(),
		nil,
	)
	if err != nil {
		return nil, err
	}

	response, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("governance client: event fetch failed with HTTP %d", response.StatusCode)
	}

	var events []coregov.GovernanceEvent
	if err := json.NewDecoder(response.Body).Decode(&events); err != nil {
		return nil, err
	}

	return events, nil
}
