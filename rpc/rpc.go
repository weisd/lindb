package rpc

import (
	"context"
	"errors"
	"strconv"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/lindb/lindb/models"
	"github.com/lindb/lindb/rpc/proto/common"
	"github.com/lindb/lindb/rpc/proto/storage"
)

//go:generate mockgen -source ./proto/storage/storage.pb.go -destination=./proto/storage/storage_mock.pb.go -package=storage
//go:generate mockgen -source ./proto/common/common.pb.go -destination=./proto/common/common_mock.pb.go -package=common
//go:generate mockgen -source ./rpc.go -destination=./rpc_mock.go -package=rpc

const (
	metaKeyLogicNode = "metaKeyLogicNode"
	metaKeyDatabase  = "metaKeyDatabase"
	metaKeyShardID   = "metaKeyShardID"
)

var (
	clientCoonFct   ClientConnFactory
	serverStreamFct ServerStreamFactory
)

func init() {
	clientCoonFct = &clientConnFactory{
		connMap: make(map[models.Node]*grpc.ClientConn),
	}

	serverStreamFct = &serverStreamFactory{
		nodeMap: make(map[models.Node]grpc.ServerStream),
	}
}

// ClientConnFactory is the factory for grpc ClientConn.
type ClientConnFactory interface {
	// GetClientConn returns the grpc ClientConn for target node.
	// One connection for a target node.
	// Concurrent safe.
	GetClientConn(target models.Node) (*grpc.ClientConn, error)
}

// clientConnFactory implements ClientConnFactory.
type clientConnFactory struct {
	// target -> connection
	connMap map[models.Node]*grpc.ClientConn
	// lock to protect connMap
	lock4map sync.Mutex
}

// GetClientConnFactory returns a singleton ClientConnFactory.
func GetClientConnFactory() ClientConnFactory {
	return clientCoonFct
}

// GetClientConn returns the grpc ClientConn for a target node.
// Concurrent safe.
func (fct *clientConnFactory) GetClientConn(target models.Node) (*grpc.ClientConn, error) {
	fct.lock4map.Lock()
	defer fct.lock4map.Unlock()

	coon, ok := fct.connMap[target]
	if ok {
		return coon, nil
	}
	conn, err := grpc.Dial(target.Indicator(), grpc.WithInsecure())
	if err != nil {
		return nil, err
	}

	fct.connMap[target] = conn

	return conn, nil
}

// ClientStreamFactory is the factory to get ClientStream.
type ClientStreamFactory interface {
	// LogicNode returns the a logic Node which will be transferred to the target server for identification.
	LogicNode() models.Node
	// CreateWriteClient creates a stream WriteClient.
	CreateWriteClient(db string, shardID int32, target models.Node) (storage.WriteService_WriteClient, error)
	// CreateQueryClient creates a stream task client
	CreateTaskClient(target models.Node) (common.TaskService_HandleClient, error)
	// CreateWriteServiceClient creates a WriteServiceClient
	CreateWriteServiceClient(target models.Node) (storage.WriteServiceClient, error)
}

// clientStreamFactory implements ClientStreamFactory.
type clientStreamFactory struct {
	logicNode models.Node
	connFct   ClientConnFactory
}

// LogicNode returns the a logic Node which will be transferred to the target server for identification.
func (w *clientStreamFactory) LogicNode() models.Node {
	return w.logicNode
}

// CreateQueryClient creates a stream task client
func (w *clientStreamFactory) CreateTaskClient(target models.Node) (common.TaskService_HandleClient, error) {
	conn, err := w.connFct.GetClientConn(target)
	if err != nil {
		return nil, err
	}

	node := w.LogicNode()
	//TODO handle context?????
	ctx := createOutgoingContextWithPairs(context.TODO(), metaKeyLogicNode, (&node).Indicator())
	cli, err := common.NewTaskServiceClient(conn).Handle(ctx)
	return cli, err
}

// CreateWriteClient creates a WriteClient.
func (w *clientStreamFactory) CreateWriteClient(db string, shardID int32,
	target models.Node) (storage.WriteService_WriteClient, error) {
	conn, err := w.connFct.GetClientConn(target)
	if err != nil {
		return nil, err
	}

	// pass logicNode.ID as meta to rpc serve
	ctx := createOutgoingContext(context.TODO(), db, shardID, w.LogicNode())
	cli, err := storage.NewWriteServiceClient(conn).Write(ctx)

	return cli, err
}

// CreateWriteServiceClient creates a WriteServiceClient
func (w *clientStreamFactory) CreateWriteServiceClient(target models.Node) (storage.WriteServiceClient, error) {
	conn, err := w.connFct.GetClientConn(target)
	if err != nil {
		return nil, err
	}
	return storage.NewWriteServiceClient(conn), nil
}

// NewClientStreamFactory returns a factory to get clientStream.
func NewClientStreamFactory(logicNode models.Node) ClientStreamFactory {
	return &clientStreamFactory{
		logicNode: logicNode,
		connFct:   GetClientConnFactory(),
	}
}

// ServerStreamFactory represents a factory to get server stream.
//TODO current only support one connection->stream, not support connection pool case
type ServerStreamFactory interface {
	// GetStream returns a ServerStream for a node.
	GetStream(node models.Node) (grpc.ServerStream, bool)
	// Register registers a stream for a node.
	Register(node models.Node, stream grpc.ServerStream)
	// Deregister unregisters a stream for node.
	Deregister(node models.Node)
	// Nodes returns all registered nodes.
	Nodes() []models.Node
}

// GetServerStreamFactory returns the singleton server stream factory
func GetServerStreamFactory() ServerStreamFactory {
	return serverStreamFct
}

type serverStreamFactory struct {
	nodeMap map[models.Node]grpc.ServerStream
	lock    sync.RWMutex
}

// GetStream returns a ServerStream for a node.
func (fct *serverStreamFactory) GetStream(node models.Node) (grpc.ServerStream, bool) {
	fct.lock.RLock()
	defer fct.lock.RUnlock()

	st, ok := fct.nodeMap[node]
	return st, ok
}

// Register registers a stream for a node.
func (fct *serverStreamFactory) Register(node models.Node, stream grpc.ServerStream) {
	fct.lock.Lock()
	defer fct.lock.Unlock()

	fct.nodeMap[node] = stream
}

// Nodes returns all registered nodes.
func (fct *serverStreamFactory) Nodes() []models.Node {
	fct.lock.RLock()
	defer fct.lock.RUnlock()

	nodes := make([]models.Node, 0, len(fct.nodeMap))
	for node := range fct.nodeMap {
		nodes = append(nodes, node)
	}
	return nodes
}

// Deregister unregisters a stream for node.
func (fct *serverStreamFactory) Deregister(node models.Node) {
	fct.lock.Lock()
	defer fct.lock.Unlock()
	delete(fct.nodeMap, node)
}

// createOutgoingContextWithPairs creates outGoing context with key, value pairs.
func createOutgoingContextWithPairs(ctx context.Context, pairs ...string) context.Context {
	return metadata.NewOutgoingContext(ctx, metadata.Pairs(pairs...))
}

// createIncomingContextWithPairs creates outGoing context with key, value pairs, mainly for test.
func createIncomingContextWithPairs(ctx context.Context, pairs ...string) context.Context {
	return metadata.NewIncomingContext(ctx, metadata.Pairs(pairs...))
}

// createOutgoingContext creates outGoing context with provided parameters.
// db is the database, shardID is the shard id for database,
// logicNode is a client provided identification on server side.
// These parameters will passed to the sever side in stream context.
func createOutgoingContext(ctx context.Context, db string, shardID int32, logicNode models.Node) context.Context {
	return metadata.AppendToOutgoingContext(ctx,
		metaKeyLogicNode, logicNode.Indicator(),
		metaKeyDatabase, db,
		metaKeyShardID, strconv.Itoa(int(shardID)))
}

// CreateIncomingContext creates incoming context with given parameters, mainly for test rpc server, mock incoming context.
func CreateIncomingContext(ctx context.Context, db string, shardID int32, logicNode models.Node) context.Context {
	return metadata.NewIncomingContext(ctx,
		metadata.Pairs(metaKeyLogicNode, logicNode.Indicator(),
			metaKeyDatabase, db,
			metaKeyShardID, strconv.Itoa(int(shardID))))
}

// CreateIncomingContext creates incoming context with given parameters, mainly for test rpc server, mock incoming context.
func CreateIncomingContextWithNode(ctx context.Context, node models.Node) context.Context {
	return createIncomingContextWithPairs(ctx, metaKeyLogicNode, node.Indicator())
}

// CreateOutgoingContext creates outgoing context with logic node.
func CreateOutgoingContextWithNode(ctx context.Context, node models.Node) context.Context {
	return createOutgoingContextWithPairs(ctx, metaKeyLogicNode, node.Indicator())
}

// getStringFromContext retrieving string metaValue from context for metaKey.
func getStringFromContext(cxt context.Context, metaKey string) (string, error) {
	md, ok := metadata.FromIncomingContext(cxt)
	if !ok {
		return "", errors.New("meta data not exists")
	}

	strList := md.Get(metaKey)

	if len(strList) != 1 {
		return "", errors.New("meta data should have exactly one string value")
	}
	return strList[0], nil
}

// GetLogicNodeFromContext returns the logicNode.
func GetLogicNodeFromContext(cxt context.Context) (*models.Node, error) {
	strVal, err := getStringFromContext(cxt, metaKeyLogicNode)
	if err != nil {
		return nil, err
	}

	return models.ParseNode(strVal)
}

// GetDatabaseFromContext returns database.
func GetDatabaseFromContext(cxt context.Context) (string, error) {
	return getStringFromContext(cxt, metaKeyDatabase)
}

// GetShardIDFromContext returns shardID.
func GetShardIDFromContext(cxt context.Context) (int32, error) {
	strVal, err := getStringFromContext(cxt, metaKeyShardID)
	if err != nil {
		return -1, err
	}

	num, err := strconv.Atoi(strVal)
	if err != nil {
		return -1, err
	}

	return int32(num), nil
}
