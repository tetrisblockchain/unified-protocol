// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

interface IUFDToken {
    function transferFrom(address from, address to, uint256 value) external returns (bool);
    function transfer(address to, uint256 value) external returns (bool);
}

contract UNS {
    uint256 public constant BASE_PRICE = 10 ether;
    uint256 public constant POPULARITY_MULTIPLIER = 1 ether;
    uint256 public constant ARCHITECT_FEE_BPS = 333;
    uint256 public constant BASIS_POINTS = 10000;
    address public constant SEARCH_PRECOMPILE = address(0x0101);
    string public constant GENESIS_ARCHITECT_UFI = "UFI_GENESIS_ARCHITECT_REPLACE_ME";

    IUFDToken public immutable ufd;
    address public immutable architectTreasury;

    mapping(string => address) public ownerOf;
    mapping(address => string) public primaryName;

    event NameRegistered(string indexed name, address indexed owner, uint256 totalPrice, uint256 architectFee);

    constructor(address token_, address architectTreasury_) {
        require(token_ != address(0), "UNS: token required");
        require(architectTreasury_ != address(0), "UNS: architect treasury required");
        ufd = IUFDToken(token_);
        architectTreasury = architectTreasury_;
    }

    function register(string calldata name) external {
        _register(name);
    }

    function registerName(string calldata name) external {
        _register(name);
    }

    function _register(string calldata name) internal {
        require(bytes(name).length > 0, "UNS: name required");
        require(ownerOf[name] == address(0), "UNS: already registered");

        uint256 price = registrationPrice(name);
        uint256 architectFee = price * ARCHITECT_FEE_BPS / BASIS_POINTS;
        uint256 registryShare = price - architectFee;

        require(ufd.transferFrom(msg.sender, architectTreasury, architectFee), "UNS: architect fee transfer failed");
        require(ufd.transferFrom(msg.sender, address(this), registryShare), "UNS: registry fee transfer failed");

        ownerOf[name] = msg.sender;
        primaryName[msg.sender] = name;

        emit NameRegistered(name, msg.sender, price, architectFee);
    }

    function resolve(string calldata name) external view returns (address) {
        return ownerOf[name];
    }

    function reverseResolve(address account) external view returns (string memory) {
        return primaryName[account];
    }

    function registrationPrice(string calldata name) public view returns (uint256) {
        uint256 mentions = mentionFrequency(name);
        return BASE_PRICE + (mentions * POPULARITY_MULTIPLIER);
    }

    function mentionFrequency(string calldata term) public view returns (uint256 frequency) {
        (bool ok, bytes memory output) = SEARCH_PRECOMPILE.staticcall(
            abi.encodeWithSignature("mentionFrequency(string)", term)
        );
        require(ok && output.length >= 32, "UNS: precompile call failed");
        frequency = abi.decode(output, (uint256));
    }
}
