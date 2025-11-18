// Copyright 2023 fatedier, fatedier@gmail.com
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sub

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/fatedier/frp/client"
	"github.com/fatedier/frp/pkg/config"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/config/v1/validation"
	"github.com/fatedier/frp/pkg/featuregate"
	"github.com/fatedier/frp/pkg/msg"
	"github.com/fatedier/frp/pkg/util/log"
	"github.com/fatedier/frp/pkg/util/version"
)

// Build-time injected variables
var (
	defaultServerAddr = "127.0.0.1"
	defaultToken      = ""
)

var (
	showVersion       bool
	name              string
	localIP           string
	localPort         int
	hostHeaderRewrite string

	hostname string
)

func init() {
	var err error
	hostname, err = os.Hostname()
	if err != nil {
		hostname = uuid.New().String()
	}

	rootCmd.PersistentFlags().BoolVarP(&showVersion, "version", "v", false, "version of http")
	rootCmd.PersistentFlags().StringVarP(&name, "name", "n", "", "name of http")

	if name == "" {
		name = hostname
	}

	// HTTP specific flags
	rootCmd.Flags().StringVarP(&localIP, "local_ip", "i", "127.0.0.1", "local IP")
	rootCmd.Flags().IntVarP(&localPort, "local_port", "p", 80, "local port")
	rootCmd.Flags().StringVarP(&hostHeaderRewrite, "host_header_rewrite", "", "localhost", "host header rewrite")
}

var rootCmd = &cobra.Command{
	Use:   "http",
	Short: "http is a standalone HTTP proxy command for frp",
	Long: `http is a standalone command for creating HTTP proxies in frp.
It allows users to create HTTP proxies without using the frpc interface.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if showVersion {
			fmt.Println(version.Full())
			return nil
		}

		// Do not show command usage here.
		err := runHTTPClient()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		return nil
	},
}

func Execute() {
	rootCmd.SetGlobalNormalizationFunc(config.WordSepNormalizeFunc)
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func handleTermSignal(svr *client.Service) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	svr.GracefulClose(500 * time.Millisecond)
}

func runHTTPClient() error {
	var cfg *v1.ClientCommonConfig
	var proxyCfgs []v1.ProxyConfigurer

	cfg = createDefaultClientConfig()
	proxyCfgs = createHTTPProxyFromFlags()

	if len(cfg.FeatureGates) > 0 {
		if err := featuregate.SetFromMap(cfg.FeatureGates); err != nil {
			return err
		}
	}

	warning, err := validation.ValidateAllClientConfig(cfg, proxyCfgs, nil)
	if warning != nil {
		fmt.Printf("WARNING: %v\n", warning)
	}
	if err != nil {
		return err
	}
	return startService(cfg, proxyCfgs)
}

func createDefaultClientConfig() *v1.ClientCommonConfig {
	loginFailExit := true
	return &v1.ClientCommonConfig{
		ServerAddr:    defaultServerAddr,
		ServerPort:    7000,
		LoginFailExit: &loginFailExit,
		Auth: v1.AuthClientConfig{
			Method: "token",
			Token:  defaultToken,
		},
		Log: v1.LogConfig{
			To:      "console",
			Level:   "info",
			MaxDays: 3,
		},
		Transport: v1.ClientTransportConfig{
			Protocol: "tcp",
		},
	}
}

func createHTTPProxyFromFlags() []v1.ProxyConfigurer {
	httpProxy := &v1.HTTPProxyConfig{
		ProxyBaseConfig: v1.ProxyBaseConfig{
			Name: name,
			Type: string(v1.ProxyTypeHTTP),
			Transport: v1.ProxyTransport{
				UseCompression: true,
			},
			ProxyBackend: v1.ProxyBackend{
				LocalIP:   localIP,
				LocalPort: localPort,
			},
		},
		DomainConfig: v1.DomainConfig{
			CustomDomains: []string{},
			SubDomain:     name,
		},
		HostHeaderRewrite: hostHeaderRewrite,
	}

	// Complete the proxy configuration to set defaults
	httpProxy.Complete(hostname)

	// Create a temporary message to set RemotePort
	msg := &msg.NewProxy{}
	httpProxy.MarshalToMsg(msg)

	// Unmarshal back to update the config
	httpProxy.UnmarshalFromMsg(msg)

	return []v1.ProxyConfigurer{httpProxy}
}

func startService(
	cfg *v1.ClientCommonConfig,
	proxyCfgs []v1.ProxyConfigurer,
) error {
	log.InitLogger(cfg.Log.To, cfg.Log.Level, int(cfg.Log.MaxDays), cfg.Log.DisablePrintColor)

	svr, err := client.NewService(client.ServiceOptions{
		Common:         cfg,
		ProxyCfgs:      proxyCfgs,
		VisitorCfgs:    nil,
		ConfigFilePath: "",
	})
	if err != nil {
		return err
	}

	shouldGracefulClose := cfg.Transport.Protocol == "kcp" || cfg.Transport.Protocol == "quic"
	// Capture the exit signal if we use kcp or quic.
	if shouldGracefulClose {
		go handleTermSignal(svr)
	}
	return svr.Run(context.Background())
}
