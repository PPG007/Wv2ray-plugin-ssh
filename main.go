package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	pb_plugin "wv2ray-plugin-template/plugin"

	"github.com/BurntSushi/toml"
	"github.com/hashicorp/go-plugin"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

const (
	PLUGIN_NAME        = "wv2ray-plugin-template"
	PLUGIN_AUTHOR      = "Your Name"
	PLUGIN_VERSION     = "v1.0.0"
	PLUGIN_DESCRIPTION = "A template plugin for WV2Ray"

	PROTOCOL_TEMPLATE = "template"

	INPUT_STRING_KEY  = "inputString"
	INPUT_NUMBER_KEY  = "inputNumber"
	INPUT_BOOL_KEY    = "inputBool"
	INPUT_MAP_KEY     = "inputMap"
	SELECT_STRING_KEY = "selectString"
	SELECT_NUMBER_KEY = "selectNumber"
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

type TemplatePlugin struct {
	pb_plugin.UnimplementedPluginOutboundServer

	bundle          *i18n.Bundle
	currentLanguage string
	localizers      map[string]*i18n.Localizer
	lock            *sync.Mutex
	handlers        sync.Map
}

type templateHandler struct {
	properties []*pb_plugin.BriefProtocolProperty
}

func NewTemplatePlugin() *TemplatePlugin {
	return &TemplatePlugin{
		localizers:      make(map[string]*i18n.Localizer),
		lock:            &sync.Mutex{},
		handlers:        sync.Map{},
		currentLanguage: pb_plugin.DEFAULT_LANGUAGE,
	}
}

func (p *TemplatePlugin) t(key string) string {
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

func (p *TemplatePlugin) Init(ctx context.Context, req *pb_plugin.EmptyRequest) (*pb_plugin.EmptyResponse, error) {
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

func (p *TemplatePlugin) SetLocale(ctx context.Context, req *pb_plugin.SetLocaleRequest) (*pb_plugin.EmptyResponse, error) {
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

func (p *TemplatePlugin) GetInfo(ctx context.Context, req *pb_plugin.EmptyRequest) (*pb_plugin.PluginInfo, error) {
	return &pb_plugin.PluginInfo{
		Name:        PLUGIN_NAME,
		Author:      PLUGIN_AUTHOR,
		Logo:        logo,
		Description: PLUGIN_DESCRIPTION,
		Version:     PLUGIN_VERSION,
		Protocols: []*pb_plugin.ProtocolConfigDetail{
			{
				Protocol: PROTOCOL_TEMPLATE,
				Properties: &pb_plugin.ProtocolConfigProperties{
					Properties: []*pb_plugin.ProtocolConfigProperty{
						{
							Field:      INPUT_STRING_KEY,
							FieldLabel: p.t("plugin.inputString"),
							FieldType:  pb_plugin.ConfigFieldType_INPUT,
							ValueType:  pb_plugin.ConfigFieldValueType_STRING,
						},
						{
							Field:      INPUT_NUMBER_KEY,
							FieldLabel: p.t("plugin.inputNumber"),
							FieldType:  pb_plugin.ConfigFieldType_INPUT,
							ValueType:  pb_plugin.ConfigFieldValueType_INT,
						},
						{
							Field:      INPUT_BOOL_KEY,
							FieldLabel: p.t("plugin.inputBool"),
							FieldType:  pb_plugin.ConfigFieldType_INPUT,
							ValueType:  pb_plugin.ConfigFieldValueType_BOOL,
						},
						{
							Field:      INPUT_MAP_KEY,
							FieldLabel: p.t("plugin.inputMap"),
							FieldType:  pb_plugin.ConfigFieldType_INPUT,
							ValueType:  pb_plugin.ConfigFieldValueType_MAP,
						},
						{
							Field:      SELECT_STRING_KEY,
							FieldLabel: p.t("plugin.selectString"),
							FieldType:  pb_plugin.ConfigFieldType_SELECT,
							ValueType:  pb_plugin.ConfigFieldValueType_STRING,
							Options: []*pb_plugin.SelectOption{
								{
									Label: p.t("plugin.selectOption1"),
									Value: &pb_plugin.SelectOption_StrValue{
										StrValue: "option1",
									},
								},
								{
									Label: p.t("plugin.selectOption2"),
									Value: &pb_plugin.SelectOption_StrValue{
										StrValue: "option2",
									},
								},
							},
						},
						{
							Field:      SELECT_NUMBER_KEY,
							FieldLabel: p.t("plugin.selectNumber"),
							FieldType:  pb_plugin.ConfigFieldType_SELECT,
							ValueType:  pb_plugin.ConfigFieldValueType_INT,
							Options: []*pb_plugin.SelectOption{
								{
									Label: p.t("plugin.selectOption1"),
									Value: &pb_plugin.SelectOption_IntValue{
										IntValue: 1,
									},
								},
								{
									Label: p.t("plugin.selectOption2"),
									Value: &pb_plugin.SelectOption_IntValue{
										IntValue: 2,
									},
								},
							},
						},
					},
				},
			},
		},
	}, nil
}

func (p *TemplatePlugin) NewHandler(ctx context.Context, req *pb_plugin.NewHandlerRequest) (*pb_plugin.EmptyResponse, error) {
	_, ok := p.handlers.Load(req.Id)
	if ok {
		return nil, ErrHandlerExists
	}
	handler := &templateHandler{
		properties: req.Properties,
	}
	p.handlers.Store(req.Id, handler)
	return &pb_plugin.EmptyResponse{}, nil
}

func (p *TemplatePlugin) ShutdownHandler(ctx context.Context, req *pb_plugin.ShutdownHandlerRequest) (*pb_plugin.EmptyResponse, error) {
	p.handlers.Delete(req.Id)
	return &pb_plugin.EmptyResponse{}, nil
}

func (p *TemplatePlugin) Handshake(stream pb_plugin.PluginOutbound_HandshakeServer) error {
	data, err := stream.Recv()
	if err != nil {
		return err
	}
	raw, ok := p.handlers.Load(data.HandlerId)
	if !ok {
		return ErrHandlerNotExists
	}
	handler := raw.(*templateHandler)
	clientConn, serverConn := net.Pipe()
	fmt.Println(handler, clientConn, serverConn) // remove this line
	go func() {
		// serverConn -> grpc stream
	}()
	go func() {
		// grpc stream -> clientConn
	}()
	// do something with clientConn and handler
	return nil
}

func (p *TemplatePlugin) Process(stream pb_plugin.PluginOutbound_ProcessServer) error {
	data, err := stream.Recv()
	if err != nil {
		return err
	}
	raw, ok := p.handlers.Load(data.HandlerId)
	if !ok {
		return ErrHandlerNotExists
	}
	handler := raw.(*templateHandler)
	fmt.Println(handler) // remove this line
	go func() {
		// destination -> grpc stream
	}()
	go func() {
		// grpc stream -> destination
	}()
	return nil
}

func main() {
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: pb_plugin.Handshake,
		Plugins: plugin.PluginSet{
			pb_plugin.PLUGIN_NAME: pb_plugin.NewPlugin(NewTemplatePlugin()),
		},
		GRPCServer: pb_plugin.NewGrpcServer,
	})
}
