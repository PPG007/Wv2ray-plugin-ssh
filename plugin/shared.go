package plugin

import (
	"context"
	"fmt"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	INITIAL_WINDOW_SIZE      int32  = 1 << 20 // 1 MB
	INITIAL_CONN_WINDOW_SIZE int32  = 1 << 20 // 1 MB
	MAX_RECV_MSG_SIZE        int    = 8 << 20 // 8 MB
	MAX_SEND_MSG_SIZE        int    = 8 << 20 // 8 MB
	MAX_CONCURRENT_STREAMS   uint32 = 1000

	PLUGIN_NAME      = "handler"
	DEFAULT_LANGUAGE = "en-US"
)

var Handshake = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "WV2RAY_PLUGIN",
	MagicCookieValue: "WV2RAY_PLUGIN",
}

type Plugin struct {
	plugin.Plugin
	server PluginOutboundServer
}

func NewPlugin(server PluginOutboundServer) *Plugin {
	p := &Plugin{
		server: server,
	}
	if server == nil {
		p.server = &UnimplementedPluginOutboundServer{}
	}
	return p
}

func (p *Plugin) GRPCClient(ctx context.Context, broker *plugin.GRPCBroker, conn *grpc.ClientConn) (interface{}, error) {
	return NewPluginOutboundClient(conn), nil
}

func (p *Plugin) GRPCServer(broker *plugin.GRPCBroker, server *grpc.Server) error {
	RegisterPluginOutboundServer(server, p.server)
	return nil
}

func unaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("%v", r)
				temp, ok := r.(error)
				if ok {
					err = temp
				}
				resp = nil
				return
			}
		}()
		return handler(ctx, req)
	}
}

func streamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("%v", r)
				temp, ok := r.(error)
				if ok {
					err = temp
				}
				return
			}
		}()
		return handler(srv, ss)
	}
}

func NewGrpcServer(opts []grpc.ServerOption) *grpc.Server {
	opts = append(opts, []grpc.ServerOption{
		grpc.InitialWindowSize(INITIAL_WINDOW_SIZE),
		grpc.InitialConnWindowSize(INITIAL_CONN_WINDOW_SIZE),
		grpc.MaxRecvMsgSize(MAX_RECV_MSG_SIZE),
		grpc.MaxSendMsgSize(MAX_SEND_MSG_SIZE),
		grpc.MaxConcurrentStreams(MAX_CONCURRENT_STREAMS),
		grpc.UnaryInterceptor(unaryInterceptor()),
		grpc.StreamInterceptor(streamInterceptor()),
	}...)
	return grpc.NewServer(opts...)
}

func GrpcClientOptions() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithInitialWindowSize(INITIAL_WINDOW_SIZE),
		grpc.WithInitialConnWindowSize(INITIAL_CONN_WINDOW_SIZE),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(MAX_RECV_MSG_SIZE),
			grpc.MaxCallSendMsgSize(MAX_SEND_MSG_SIZE),
		),
	}
}
