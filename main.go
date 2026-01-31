package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	pb_plugin "wv2ray-plugin-template/plugin"

	"github.com/BurntSushi/toml"
	"github.com/hashicorp/go-plugin"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/crypto/ssh"
	"golang.org/x/text/language"
)

const (
	PLUGIN_NAME        = "wv2ray-plugin-ssh"
	PLUGIN_AUTHOR      = "PPG007"
	PLUGIN_VERSION     = "v1.0.0"
	PLUGIN_DESCRIPTION = "A SSH plugin for WV2Ray"

	PROTOCOL_SSH = "ssh"

	USERNAME_KEY = "username"
	PASSWORD_KEY = "password"

	DEFAULT_TIMEOUT = 5 * time.Second
)

//go:embed locales/*.toml
var localesFS embed.FS

//go:embed logo.png
var logo []byte

var (
	ErrBundleNotInitialized = errors.New("bundle not initialized")
	ErrHandlerExists        = errors.New("handler exists")
	ErrHandlerNotExists     = errors.New("handler not exists")
	ErrHandlerNotReady      = errors.New("handler not ready")
	ErrUnsupportedProtocol  = errors.New("unsupported protocol")
)

type SSHPlugin struct {
	pb_plugin.UnimplementedPluginOutboundServer

	bundle          *i18n.Bundle
	currentLanguage string
	localizers      map[string]*i18n.Localizer
	lock            *sync.Mutex
	handlers        sync.Map
	pool            sync.Pool
}

type sshHandler struct {
	id         string
	ready      bool
	client     *ssh.Client
	properties []*pb_plugin.BriefProtocolProperty
}

func (s *sshHandler) getUsername() string {
	for _, prop := range s.properties {
		if prop.Field == USERNAME_KEY {
			return prop.Value.GetStrValue()
		}
	}
	return ""
}

func (s *sshHandler) getPassword() string {
	for _, prop := range s.properties {
		if prop.Field == PASSWORD_KEY {
			return prop.Value.GetStrValue()
		}
	}
	return ""
}

func (s *sshHandler) reset() {
	s.id = ""
	s.ready = false
	s.client = nil
	s.properties = []*pb_plugin.BriefProtocolProperty{}
}

func NewSSHPlugin() *SSHPlugin {
	return &SSHPlugin{
		localizers:      make(map[string]*i18n.Localizer),
		lock:            &sync.Mutex{},
		handlers:        sync.Map{},
		currentLanguage: pb_plugin.DEFAULT_LANGUAGE,
		pool: sync.Pool{
			New: func() any {
				return &sshHandler{}
			},
		},
	}
}

func (p *SSHPlugin) t(key string) string {
	localizer := p.localizers[p.currentLanguage]
	if localizer == nil {
		localizer = i18n.NewLocalizer(p.bundle, p.currentLanguage)
		fn := func() {
			p.lock.Lock()
			defer p.lock.Unlock()
			p.localizers[p.currentLanguage] = localizer
		}
		fn()
	}
	message, err := localizer.Localize(&i18n.LocalizeConfig{
		MessageID: key,
	})
	if err != nil {
		return key
	}
	return message
}

func (p *SSHPlugin) HealthCheck(ctx context.Context, req *pb_plugin.EmptyRequest) (*pb_plugin.EmptyResponse, error) {
	return &pb_plugin.EmptyResponse{}, nil
}

func (p *SSHPlugin) Init(ctx context.Context, req *pb_plugin.EmptyRequest) (*pb_plugin.EmptyResponse, error) {
	defaultLang, err := language.Parse(pb_plugin.DEFAULT_LANGUAGE)
	if err != nil {
		return nil, err
	}
	bundle := i18n.NewBundle(defaultLang)
	bundle.RegisterUnmarshalFunc("toml", toml.Unmarshal)
	entries, err := localesFS.ReadDir("locales")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".toml") {
			_, err := bundle.LoadMessageFileFS(localesFS, "locales/"+entry.Name())
			if err != nil {
				return nil, fmt.Errorf("failed to load locale file %s: %w", entry.Name(), err)
			}
		}
	}
	p.bundle = bundle
	return &pb_plugin.EmptyResponse{}, nil
}

func (p *SSHPlugin) SetLocale(ctx context.Context, req *pb_plugin.SetLocaleRequest) (*pb_plugin.EmptyResponse, error) {
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.bundle == nil {
		return nil, ErrBundleNotInitialized
	}
	p.currentLanguage = req.Language
	localizer, exists := p.localizers[req.Language]
	if exists {
		return &pb_plugin.EmptyResponse{}, nil
	}
	localizer = i18n.NewLocalizer(p.bundle, req.Language)
	p.localizers[req.Language] = localizer
	return &pb_plugin.EmptyResponse{}, nil
}

func (p *SSHPlugin) GetInfo(ctx context.Context, req *pb_plugin.EmptyRequest) (*pb_plugin.PluginInfo, error) {
	return &pb_plugin.PluginInfo{
		Name:        PLUGIN_NAME,
		Author:      PLUGIN_AUTHOR,
		Logo:        logo,
		Description: PLUGIN_DESCRIPTION,
		Version:     PLUGIN_VERSION,
		Protocols: []*pb_plugin.ProtocolConfigDetail{
			{
				Protocol: PROTOCOL_SSH,
				Properties: &pb_plugin.ProtocolConfigProperties{
					Properties: []*pb_plugin.ProtocolConfigProperty{
						{
							Field:      USERNAME_KEY,
							FieldLabel: p.t("plugin.username"),
							FieldType:  pb_plugin.ConfigFieldType_INPUT,
							ValueType:  pb_plugin.ConfigFieldValueType_STRING,
							Required:   true,
						},
						{
							Field:      PASSWORD_KEY,
							FieldLabel: p.t("plugin.password"),
							FieldType:  pb_plugin.ConfigFieldType_INPUT,
							ValueType:  pb_plugin.ConfigFieldValueType_STRING,
						},
					},
				},
			},
		},
	}, nil
}

func (p *SSHPlugin) NewHandler(ctx context.Context, req *pb_plugin.NewHandlerRequest) (*pb_plugin.EmptyResponse, error) {
	_, ok := p.handlers.Load(req.Id)
	if ok {
		return nil, ErrHandlerExists
	}
	handler := p.pool.Get().(*sshHandler)
	handler.id = req.Id
	handler.properties = req.Properties
	p.handlers.Store(req.Id, handler)
	return &pb_plugin.EmptyResponse{}, nil
}

func (p *SSHPlugin) ShutdownHandler(ctx context.Context, req *pb_plugin.ShutdownHandlerRequest) (*pb_plugin.EmptyResponse, error) {
	rawHandler, ok := p.handlers.Load(req.Id)
	if !ok {
		return nil, ErrHandlerNotExists
	}
	handler := rawHandler.(*sshHandler)
	if handler.client != nil {
		handler.client.Close()
	}
	p.handlers.Delete(req.Id)
	handler.reset()
	p.pool.Put(handler)
	return &pb_plugin.EmptyResponse{}, nil
}

func (p *SSHPlugin) Handshake(stream pb_plugin.PluginOutbound_HandshakeServer) error {
	data, err := stream.Recv()
	if err != nil {
		return err
	}
	raw, ok := p.handlers.Load(data.HandlerId)
	if !ok {
		return ErrHandlerNotExists
	}
	var (
		handler                = raw.(*sshHandler)
		clientConn, serverConn = net.Pipe()
		errChan                = make(chan error, 5)
	)
	defer serverConn.Close()
	defer clientConn.Close()
	go func() {
		// serverConn -> grpc stream
		buffer := make([]byte, 8*1024)
		for {
			n, err := clientConn.Read(buffer)
			if err != nil {
				errChan <- err
				return
			}
			err = stream.Send(&pb_plugin.HandshakeData{
				HandlerId: handler.id,
				Data:      buffer[:n],
			})
			if err != nil {
				errChan <- err
				return
			}
		}
	}()
	go func() {
		// grpc stream -> clientConn
		for {
			data, err := stream.Recv()
			if err != nil {
				errChan <- err
				return
			}
			if len(data.Data) > 0 {
				_, err = clientConn.Write(data.Data)
				if err != nil {
					errChan <- err
					return
				}
			}
		}
	}()
	sshConn, chans, reqs, err := ssh.NewClientConn(serverConn, "", &ssh.ClientConfig{
		User: handler.getUsername(),
		Auth: []ssh.AuthMethod{
			ssh.Password(handler.getPassword()),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         DEFAULT_TIMEOUT,
	})
	if err != nil {
		return err
	}
	client := ssh.NewClient(sshConn, chans, reqs)
	handler.ready = true
	handler.client = client
	err = stream.Send(&pb_plugin.HandshakeData{
		HandlerId: handler.id,
		Ready:     true,
	})
	if err != nil {
		return err
	}
	go ssh.DiscardRequests(reqs)
	err = <-errChan
	return err
}

func (p *SSHPlugin) Process(stream pb_plugin.PluginOutbound_ProcessServer) error {
	data, err := stream.Recv()
	if err != nil {
		return err
	}
	raw, ok := p.handlers.Load(data.HandlerId)
	if !ok {
		return ErrHandlerNotExists
	}
	handler := raw.(*sshHandler)
	if !handler.ready {
		return ErrHandlerNotReady
	}
	sshChannel, reqs, err := handler.client.OpenChannel("direct-tcpip", ssh.Marshal(&struct {
		Host       string
		Port       uint32
		OriginAddr string
		OriginPort uint32
	}{
		Host:       data.DestAddr,
		Port:       uint32(data.DestPort),
		OriginAddr: "127.0.0.1",
		OriginPort: 0,
	}))
	if err != nil {
		return err
	}
	defer sshChannel.Close()
	go ssh.DiscardRequests(reqs)
	errChan := make(chan error, 5)
	go func() {
		// destination -> grpc stream
		buffer := make([]byte, 8*1024)
		for {
			n, err := sshChannel.Read(buffer)
			if err != nil {
				errChan <- err
				return
			}
			err = stream.Send(&pb_plugin.TransportData{
				HandlerId: data.HandlerId,
				Data:      buffer[:n],
			})
			if err != nil {
				errChan <- err
				return
			}
		}
	}()
	go func() {
		// grpc stream -> destination
		if len(data.Data) > 0 {
			_, err := sshChannel.Write(data.Data)
			if err != nil {
				errChan <- err
				return
			}
		}
		for {
			data, err := stream.Recv()
			if err != nil {
				errChan <- err
				return
			}
			if len(data.Data) > 0 {
				_, err = sshChannel.Write(data.Data)
				if err != nil {
					errChan <- err
					return
				}
			}
		}
	}()
	err = <-errChan
	return err
}

func main() {
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: pb_plugin.Handshake,
		Plugins: plugin.PluginSet{
			pb_plugin.PLUGIN_NAME: pb_plugin.NewPlugin(NewSSHPlugin()),
		},
		GRPCServer: pb_plugin.NewGrpcServer,
	})
}
