package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/lindb/lindb/constants"
	"github.com/lindb/lindb/coordinator/discovery"
	"github.com/lindb/lindb/models"
	"github.com/lindb/lindb/pkg/logger"
	"github.com/lindb/lindb/pkg/pathutil"
	"github.com/lindb/lindb/rpc"
)

//go:generate mockgen -source=./storage_state_machine.go -destination=./storage_state_machine_mock.go -package=broker

// StorageStateMachine represents storage cluster state state machine.
// Each broker node will start this state machine which watch storage cluster state change event.
type StorageStateMachine interface {
	discovery.Listener
	// List lists currently all alive storage cluster's state
	List() []*models.StorageState
	// Close closes state machine, stops watch change event
	Close() error
}

// storageStateMachine implements storage state state machine interface
type storageStateMachine struct {
	discovery     discovery.Discovery
	ctx           context.Context
	cancel        context.CancelFunc
	streamFactory rpc.ClientStreamFactory

	storageClusters map[string]*StorageClusterState

	mutex sync.RWMutex

	log *logger.Logger
}

// NewStorageStateMachine creates state machine, init data if exist, then starts watch change event
func NewStorageStateMachine(ctx context.Context,
	discoveryFactory discovery.Factory, streamFactory rpc.ClientStreamFactory) (StorageStateMachine, error) {
	c, cancel := context.WithCancel(ctx)
	log := logger.GetLogger("/broker/storage/sm")
	stateMachine := &storageStateMachine{
		streamFactory:   streamFactory,
		ctx:             c,
		cancel:          cancel,
		storageClusters: make(map[string]*StorageClusterState),
		log:             log,
	}
	repo := discoveryFactory.GetRepo()
	clusterList, err := repo.List(c, constants.StorageClusterStatePath)
	if err != nil {
		return nil, fmt.Errorf("get storage cluster state list error:%s", err)
	}

	// init exist cluster list
	for _, cluster := range clusterList {
		stateMachine.addCluster(cluster)
	}
	// new storage config discovery
	stateMachine.discovery = discoveryFactory.CreateDiscovery(constants.StorageClusterStatePath, stateMachine)
	if err := stateMachine.discovery.Discovery(); err != nil {
		return nil, fmt.Errorf("discovery storage cluster state error:%s", err)
	}
	log.Info("state machine started")
	return stateMachine, nil
}

// List lists currently all alive storage cluster's state
func (s *storageStateMachine) List() []*models.StorageState {
	var result []*models.StorageState
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	for _, storageState := range s.storageClusters {
		result = append(result, storageState.state)
	}

	return result
}

// OnCreate modifies storage cluster's state, such as trigger by node online/offline event
func (s *storageStateMachine) OnCreate(key string, resource []byte) {
	s.addCluster(resource)
}

// OnDelete deletes storage cluster's state when cluster offline
func (s *storageStateMachine) OnDelete(key string) {
	name := pathutil.GetName(key)
	s.mutex.Lock()
	defer s.mutex.Unlock()

	state, ok := s.storageClusters[name]
	if ok {
		state.close()
		delete(s.storageClusters, name)
	}
}

// Close closes state machine, stops watch change event
func (s *storageStateMachine) Close() error {
	s.discovery.Close()

	s.mutex.Lock()
	defer s.mutex.Unlock()

	// close all storage cluster's states
	for _, state := range s.storageClusters {
		state.close()
	}

	s.storageClusters = make(map[string]*StorageClusterState)
	s.cancel()
	return nil
}

// addCluster creates and starts cluster controller, if success cache it
func (s *storageStateMachine) addCluster(resource []byte) {
	storageState := models.NewStorageState()
	if err := json.Unmarshal(resource, storageState); err != nil {
		s.log.Error("discovery new storage state but unmarshal error",
			logger.String("data", string(resource)), logger.Error(err))
		return
	}
	if len(storageState.Name) == 0 {
		s.log.Error("cluster name is empty")
		return
	}
	s.log.Info("storage cluster state change", logger.String("cluster", storageState.Name))
	s.mutex.Lock()
	defer s.mutex.Unlock()

	//TODO need check if same state, maybe state is same, such as system start
	state, ok := s.storageClusters[storageState.Name]
	if !ok {
		state = newStorageClusterState(s.streamFactory)
		s.storageClusters[storageState.Name] = state
	}
	state.SetState(storageState)
}
