// SPDX-License-Identifier: MIT
pragma solidity ^0.8.19;

import {Ownable} from "@openzeppelin/contracts/access/Ownable.sol";

interface IProjectStore {
    function mint(address _owner) external returns (uint256 projectId_);
}

contract ProjectRegistrar is Ownable {
    event ProjectRegistered(uint256 indexed projectId);
    event RegistrationFeeSet(uint256 fee);
    event FeeWithdrawn(address indexed account, uint256 amount);

    uint256 public registrationFee;
    IProjectStore public immutable projectStore;

    constructor(address _projectStore) {
        projectStore = IProjectStore(_projectStore);
    }

    function setRegistrationFee(uint256 _fee) public onlyOwner {
        registrationFee = _fee;
        emit RegistrationFeeSet(_fee);
    }

    function register() external payable returns (uint256) {
        require(msg.value >= registrationFee, "insufficient fee");
        return projectStore.mint(msg.sender);
    }

    function withdrawFee(address payable _account, uint256 _amount) external onlyOwner {
        (bool success, ) = _account.call{value: _amount}("");
        require(success, "withdraw fee fail");

        emit FeeWithdrawn(_account, _amount);
    }
}
