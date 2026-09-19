package main

import (
	"bytes"
	"compress/flate"
	"context"
	"embed"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
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
	PLUGIN_NAME        = "Wv2ray-plugin-ssh"
	PLUGIN_AUTHOR      = "PPG007"
	PLUGIN_VERSION     = "v1.2.0"
	PLUGIN_DESCRIPTION = "A SSH plugin for Wv2Ray"

	PROTOCOL_SSH = "ssh"

	USERNAME_KEY = "username"
	PASSWORD_KEY = "password"
	SSH_KEY_KEY  = "key"

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
	ErrNoAuthMethod         = errors.New("neither ssh key nor password is configured")
	ErrInvalidKey           = errors.New("invalid ssh key")
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
	return getPropertyValue(s.properties, USERNAME_KEY)
}

func (s *sshHandler) getPassword() string {
	return getPropertyValue(s.properties, PASSWORD_KEY)
}

func (s *sshHandler) getKey() string {
	return getPropertyValue(s.properties, SSH_KEY_KEY)
}

func getPropertyValue(properties []*pb_plugin.BriefProtocolProperty, key string) string {
	for _, prop := range properties {
		if prop.Field == key {
			return prop.Value.GetStrValue()
		}
	}
	return ""
}

func compressPrivateKey(key string) (string, string, error) {
	block, _ := pem.Decode([]byte(key))
	if block == nil {
		return "", "", ErrInvalidKey
	}
	var (
		buf = &bytes.Buffer{}
	)
	w, err := flate.NewWriter(buf, flate.BestCompression)
	if err != nil {
		return "", "", err
	}
	_, err = w.Write(block.Bytes)
	if err != nil {
		return "", "", err
	}
	err = w.Close()
	if err != nil {
		return "", "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf.Bytes()), block.Type, nil
}

func decompressPrivateKey(key, label string) (string, error) {
	compressed, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil {
		return "", err
	}
	reader := flate.NewReader(bytes.NewReader(compressed))
	defer reader.Close()
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	block := &pem.Block{
		Type:  label,
		Bytes: decompressed,
	}
	return string(pem.EncodeToMemory(block)), nil
}

// getAuthMethods builds the auth method list, ssh key first and password as
// fallback. When the key is encrypted, the password is used as its passphrase.
func (s *sshHandler) getAuthMethods() ([]ssh.AuthMethod, error) {
	var (
		methods  []ssh.AuthMethod
		key      = s.getKey()
		password = s.getPassword()
	)
	if key != "" {
		signer, err := ssh.ParsePrivateKey([]byte(key))
		var passphraseErr *ssh.PassphraseMissingError
		if errors.As(err, &passphraseErr) && password != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(key), []byte(password))
		}
		if err != nil {
			return nil, fmt.Errorf("failed to parse ssh key: %w", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if password != "" {
		methods = append(methods, ssh.Password(password))
	}
	if len(methods) == 0 {
		return nil, ErrNoAuthMethod
	}
	return methods, nil
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
						},
						{
							Field:      PASSWORD_KEY,
							FieldLabel: p.t("plugin.password"),
							FieldType:  pb_plugin.ConfigFieldType_INPUT,
							ValueType:  pb_plugin.ConfigFieldValueType_STRING,
						},
						{
							Field:      SSH_KEY_KEY,
							FieldLabel: p.t("plugin.key"),
							FieldType:  pb_plugin.ConfigFieldType_TEXTAREA,
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
	authMethods, err := handler.getAuthMethods()
	if err != nil {
		return err
	}
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
		User:            handler.getUsername(),
		Auth:            authMethods,
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

func (p *SSHPlugin) ParseLink(ctx context.Context, req *pb_plugin.ParseLinkRequest) (*pb_plugin.BriefConnection, error) {
	sshUrl, err := url.Parse(req.Link)
	if err != nil {
		return nil, err
	}
	if sshUrl.Scheme != PROTOCOL_SSH {
		return nil, ErrUnsupportedProtocol
	}
	port, err := strconv.Atoi(sshUrl.Port())
	if err != nil {
		return nil, err
	}
	user := sshUrl.User
	if user == nil {
		return nil, ErrNoAuthMethod
	}
	password, _ := user.Password()
	decompressed, _ := decompressPrivateKey(password, sshUrl.Query().Get("label"))
	if decompressed == "" {
		decompressed = password
	}

	return &pb_plugin.BriefConnection{
		Protocol: PROTOCOL_SSH,
		Name:     sshUrl.Fragment,
		Address:  sshUrl.Hostname(),
		Port:     int64(port),
		Properties: []*pb_plugin.BriefProtocolProperty{
			{
				Field: USERNAME_KEY,
				Value: &pb_plugin.ConfigFieldValue{
					Value: &pb_plugin.ConfigFieldValue_StrValue{
						StrValue: user.Username(),
					},
				},
			},
			{
				Field: func() string {
					if decompressed != password {
						return SSH_KEY_KEY
					}
					return PASSWORD_KEY
				}(),
				Value: &pb_plugin.ConfigFieldValue{
					Value: &pb_plugin.ConfigFieldValue_StrValue{
						StrValue: decompressed,
					},
				},
			},
		},
	}, nil
}

func (p *SSHPlugin) SerializeLink(ctx context.Context, req *pb_plugin.BriefConnection) (*pb_plugin.SerializeLinkResponse, error) {
	var (
		username = getPropertyValue(req.Properties, USERNAME_KEY)
		password = getPropertyValue(req.Properties, PASSWORD_KEY)
		priv     = getPropertyValue(req.Properties, SSH_KEY_KEY)
		query    = url.Values{}
	)
	if password == "" && priv != "" {
		compressed, label, err := compressPrivateKey(priv)
		if err != nil {
			return nil, err
		}
		password = compressed
		query.Set("label", label)
	}
	sshUrl := url.URL{
		Scheme:   PROTOCOL_SSH,
		Host:     fmt.Sprintf("%s:%d", req.Address, req.Port),
		User:     url.UserPassword(username, password),
		RawQuery: query.Encode(),
		Fragment: req.Name,
	}

	return &pb_plugin.SerializeLinkResponse{
		Link: sshUrl.String(),
	}, nil
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
