// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

interface IUFDToken {
    function balanceOf(address account) external view returns (uint256);
    function totalSupply() external view returns (uint256);
    function transfer(address to, uint256 value) external returns (bool);
    function transferFrom(address from, address to, uint256 value) external returns (bool);
}

interface IUnifiedNameService {
    function resolve(string calldata name) external view returns (address);
    function reverseResolve(address account) external view returns (string memory);
}

interface ICrawledDataIndex {
    function mentionCount(string calldata term) external view returns (uint256);
}

contract Governor {
    uint256 public constant PROPOSAL_THRESHOLD = 1000 ether;
    uint256 public constant PROPOSAL_DURATION_BLOCKS = 40320;
    uint256 public constant QUORUM_BPS = 1000;
    uint256 public constant ARCHITECT_FEE_BPS = 333;
    uint256 public constant BASIS_POINTS = 10000;
    address public constant BURN_ADDRESS = 0x000000000000000000000000000000000000dEaD;
    string public constant GENESIS_ARCHITECT_UFI = "UFI_GENESIS_ARCHITECT_REPLACE_ME";

    IUFDToken public immutable ufd;
    IUnifiedNameService public immutable uns;
    ICrawledDataIndex public immutable crawledDataIndex;
    address public immutable architectTreasury;

    uint256 public proposalCount;

    struct ProposalInput {
        string title;
        string proposerAlias;
        string targetComponent;
        string logicExtension;
        bytes executionPayload;
        uint256 stakeAmount;
    }

    struct Proposal {
        uint256 id;
        address proposer;
        string proposerAlias;
        string title;
        string targetComponent;
        string logicExtension;
        bytes executionPayload;
        uint256 stake;
        uint256 startBlock;
        uint256 endBlock;
        uint256 forVotes;
        uint256 againstVotes;
        uint256 abstainVotes;
        bool executed;
        bool canceled;
        bool rejected;
    }

    mapping(uint256 => Proposal) private proposals;
    mapping(uint256 => mapping(address => bool)) public hasVoted;
    mapping(address => uint256) public lockedStake;

    event ProposalCreated(
        uint256 indexed id,
        address indexed proposer,
        string title,
        string targetComponent,
        uint256 stake,
        uint256 startBlock,
        uint256 endBlock
    );
    event VoteCast(uint256 indexed proposalId, address indexed voter, uint8 support, uint256 weight);
    event ProposalFinalized(uint256 indexed proposalId, bool passed, bool rejected, uint256 slashAmount);
    event GovernanceEvent(uint256 indexed id, string sector, uint256 multiplier);

    constructor(address token_, address uns_, address crawledDataIndex_, address architectTreasury_) {
        require(token_ != address(0), "Governor: token required");
        require(uns_ != address(0), "Governor: UNS required");
        require(crawledDataIndex_ != address(0), "Governor: crawled data index required");
        require(architectTreasury_ != address(0), "Governor: architect treasury required");

        ufd = IUFDToken(token_);
        uns = IUnifiedNameService(uns_);
        crawledDataIndex = ICrawledDataIndex(crawledDataIndex_);
        architectTreasury = architectTreasury_;
    }

    function propose(ProposalInput calldata input) external returns (uint256 proposalId) {
        require(bytes(input.title).length > 0, "Governor: title required");
        require(_isUGPTitle(input.title), "Governor: title must use UGP-XXX format");
        require(bytes(input.targetComponent).length > 0, "Governor: target component required");
        require(bytes(input.logicExtension).length > 0, "Governor: logic extension required");
        require(input.executionPayload.length > 0, "Governor: execution payload required");
        require(_holdsThreshold(msg.sender) || input.stakeAmount >= PROPOSAL_THRESHOLD, "Governor: threshold not met");
        _validateExecutionPayload(input.executionPayload);

        if (bytes(input.proposerAlias).length > 0) {
            require(uns.resolve(input.proposerAlias) == msg.sender, "Governor: UNS alias mismatch");
        }

        if (input.stakeAmount > 0) {
            require(ufd.transferFrom(msg.sender, address(this), input.stakeAmount), "Governor: stake transfer failed");
            lockedStake[msg.sender] += input.stakeAmount;
        }

        proposalId = ++proposalCount;
        (uint256 startBlock, uint256 endBlock) = _initializeProposal(proposalId, msg.sender, input);

        emit ProposalCreated(proposalId, msg.sender, input.title, input.targetComponent, input.stakeAmount, startBlock, endBlock);
    }

    function castVote(uint256 proposalId, uint8 support) external {
        Proposal storage proposal = proposals[proposalId];
        require(proposal.id != 0, "Governor: unknown proposal");
        require(block.number <= proposal.endBlock, "Governor: voting closed");
        require(!hasVoted[proposalId][msg.sender], "Governor: already voted");
        require(support <= 2, "Governor: invalid support");

        uint256 weight = ufd.balanceOf(msg.sender);
        require(weight > 0, "Governor: no voting power");

        hasVoted[proposalId][msg.sender] = true;

        if (support == 0) {
            proposal.againstVotes += weight;
        } else if (support == 1) {
            proposal.forVotes += weight;
        } else {
            proposal.abstainVotes += weight;
        }

        emit VoteCast(proposalId, msg.sender, support, weight);
    }

    function finalize(uint256 proposalId) external {
        Proposal storage proposal = proposals[proposalId];
        require(proposal.id != 0, "Governor: unknown proposal");
        require(block.number > proposal.endBlock, "Governor: proposal active");
        require(!proposal.executed, "Governor: already executed");
        require(!proposal.canceled, "Governor: canceled");

        uint256 quorumVotes = circulatingSupply() * QUORUM_BPS / BASIS_POINTS;
        bool passed = proposal.forVotes > proposal.againstVotes && _participation(proposal) >= quorumVotes;

        if (passed) {
            proposal.executed = true;
            _releaseStake(proposal.proposer, proposal.stake);
            (string memory sector, uint256 multiplier) = abi.decode(proposal.executionPayload, (string, uint256));
            emit GovernanceEvent(proposalId, sector, multiplier);
            emit ProposalFinalized(proposalId, true, false, 0);
            return;
        }

        proposal.rejected = true;
        uint256 slashAmount = proposal.stake;
        if (slashAmount > 0) {
            lockedStake[proposal.proposer] -= slashAmount;

            uint256 architectCut = slashAmount * ARCHITECT_FEE_BPS / BASIS_POINTS;
            uint256 burnAmount = slashAmount - architectCut;

            require(ufd.transfer(architectTreasury, architectCut), "Governor: architect payout failed");
            require(ufd.transfer(BURN_ADDRESS, burnAmount), "Governor: burn failed");
        }

        emit ProposalFinalized(proposalId, false, true, slashAmount);
    }

    function circulatingSupply() public view returns (uint256) {
        return ufd.totalSupply() - ufd.balanceOf(address(0)) - ufd.balanceOf(BURN_ADDRESS);
    }

    function usernamePopularity(string calldata username) external view returns (uint256) {
        return crawledDataIndex.mentionCount(username);
    }

    function proposalMeta(uint256 proposalId)
        external
        view
        returns (
            string memory title,
            string memory proposerAlias,
            string memory targetComponent,
            string memory logicExtension,
            uint256 quorumVotes,
            uint256 popularityMentions
        )
    {
        Proposal storage proposal = proposals[proposalId];
        require(proposal.id != 0, "Governor: unknown proposal");

        title = proposal.title;
        proposerAlias = proposal.proposerAlias;
        targetComponent = proposal.targetComponent;
        logicExtension = proposal.logicExtension;
        quorumVotes = circulatingSupply() * QUORUM_BPS / BASIS_POINTS;
        popularityMentions = crawledDataIndex.mentionCount(proposal.title);
    }

    function _holdsThreshold(address account) internal view returns (bool) {
        return ufd.balanceOf(account) + lockedStake[account] >= PROPOSAL_THRESHOLD;
    }

    function _participation(Proposal storage proposal) internal view returns (uint256) {
        return proposal.forVotes + proposal.againstVotes + proposal.abstainVotes;
    }

    function _releaseStake(address proposer, uint256 amount) internal {
        if (amount == 0) {
            return;
        }

        lockedStake[proposer] -= amount;
        require(ufd.transfer(proposer, amount), "Governor: stake refund failed");
    }

    function _initializeProposal(
        uint256 proposalId,
        address proposer,
        ProposalInput calldata input
    ) internal returns (uint256 startBlock, uint256 endBlock) {
        Proposal storage proposal = proposals[proposalId];
        startBlock = block.number;
        endBlock = block.number + PROPOSAL_DURATION_BLOCKS;

        proposal.id = proposalId;
        proposal.proposer = proposer;
        proposal.proposerAlias = input.proposerAlias;
        proposal.title = input.title;
        proposal.targetComponent = input.targetComponent;
        proposal.logicExtension = input.logicExtension;
        proposal.executionPayload = input.executionPayload;
        proposal.stake = input.stakeAmount;
        proposal.startBlock = startBlock;
        proposal.endBlock = endBlock;
    }

    function _validateExecutionPayload(bytes calldata executionPayload) internal pure {
        (string memory sector, uint256 multiplier) = abi.decode(executionPayload, (string, uint256));
        require(bytes(sector).length > 0, "Governor: sector required");
        require(multiplier > 0, "Governor: multiplier required");
    }

    function _isUGPTitle(string memory title) internal pure returns (bool) {
        bytes memory data = bytes(title);
        if (data.length < 7) {
            return false;
        }

        return
            data[0] == bytes1("U") &&
            data[1] == bytes1("G") &&
            data[2] == bytes1("P") &&
            data[3] == bytes1("-");
    }
}
