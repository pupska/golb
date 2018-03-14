package main

import (
	"github.com/hashicorp/raft"
	"github.com/hashicorp/raft-boltdb"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"
)

type node struct {
	config *Config
	raft   *raft.Raft
	fsm    *fsm
}

func NewNode(config *Config) (*node, error) {
	fsm := &fsm{
		stateValue:    0,
		webLeaderAddr: "",
	}

	if err := os.MkdirAll(config.DataDir, 0700); err != nil {
		return nil, err
	}

	raftConfig := raft.DefaultConfig()
	//raftConfig.HeartbeatTimeout =
	raftConfig.LocalID = raft.ServerID(config.RaftAddress.String())
	raftConfig.Logger = log.New(os.Stdout, "raft-logger", 0)

	transport, err := raftTransport(config.RaftAddress)
	if err != nil {
		return nil, err
	}

	snapshotStore, err := raft.NewFileSnapshotStore(config.DataDir, 1, nil)
	if err != nil {
		return nil, err
	}

	logStore, err := raftboltdb.NewBoltStore(filepath.Join(config.DataDir, "raft-log.bolt"))
	if err != nil {
		return nil, err
	}

	stableStore, err := raftboltdb.NewBoltStore(filepath.Join(config.DataDir, "raft-stable.bolt"))
	if err != nil {
		return nil, err
	}

	raftNode, err := raft.NewRaft(raftConfig, fsm, logStore, stableStore,
		snapshotStore, transport)
	if err != nil {
		return nil, err
	}
	if config.Bootstrap {
		configuration := raft.Configuration{
			Servers: []raft.Server{
				{
					ID:      raftConfig.LocalID,
					Address: transport.LocalAddr(),
				},
			},
		}
		raftNode.BootstrapCluster(configuration)
	}
	return &node{
		config: config,
		raft:   raftNode,
		fsm:    fsm,
	}, nil
}

func raftTransport(raftAddr net.Addr) (*raft.NetworkTransport, error) {
	address, err := net.ResolveTCPAddr("tcp", raftAddr.String())
	if err != nil {
		return nil, err
	}

	transport, err := raft.NewTCPTransport(address.String(), address, 3, 10*time.Second, os.Stdout)
	if err != nil {
		return nil, err
	}

	return transport, nil
}
