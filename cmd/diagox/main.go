// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"

	"github.com/emiago/diago/media"
	"github.com/emiago/sipgo/sip"
	"github.com/emiago/diagox"
	"github.com/emiago/diagox/cmd"
)

var (
	envConf diagox.EnvConfig
	log     *slog.Logger
)

var (
	nodeID string
)

func init() {
	cmd.InitLogger()
	log = slog.Default()
}

func main() {
	if err := diagox.EnvConfigLoad(&envConf); err != nil {
		panic(err)
	}
	var (
		fbindHostPort = flag.String("l", "127.0.0.1:5060", "SIP listen Addr")
		debugInfo     = flag.Bool("buildinfo", false, "Show build info")
	)
	flag.Usage = func() {
		// TODO we do not want to share current glas
		fmt.Fprintln(os.Stderr, "Welcome to diagox server")
		flag.PrintDefaults()
		return
	}
	flag.Parse()

	if info, ok := debug.ReadBuildInfo(); ok && *debugInfo {
		fmt.Fprintln(os.Stderr, info.String())
		return
	}

	// Create transaction users, as many as needed.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// envConfData, _ := json.MarshalIndent(envConf, "", " ")
	log.Debug("loaded enviroment conf", "conf", envConf)

	// sipgox.RTPPortStart = 5000
	// sipgox.RTPPortEnd = 6000
	sip.SIPDebug = envConf.SIPDebug
	media.RTCPDebug = envConf.RTCPDebug
	media.RTPDebug = envConf.RTPDebug

	// if file, err := os.Open("gopbx.yml"); err == nil {
	// 	if err := ConfigLoad(file, &conf); err != nil {
	// 		log.Fatal().Err(err).Msg("Failed to load config")
	// 	}
	// }

	host, port, err := sip.ParseAddr(*fbindHostPort)
	if err != nil {
		log.Error("Parsing bind host port failed", "error", err)
		return
	}

	conf := diagox.Config{
		Transports: map[string]diagox.ConfigTransport{
			"udp": {
				Transport: "udp",
				Bind:      host,
				Port:      port,
			},
		},
	}

	if err := diagox.ConfigLoadYamlFile(envConf.ConfFile, &conf); err != nil {
		log.Error("Failed to load config file", "error", err)
	}

	if err := conf.Validate(); err != nil {
		log.Error("Invalid config file", "error", err)
	}

	// confData, _ := json.MarshalIndent(conf, "", " ")
	log.Debug("loaded conf", "conf", conf)
	pbx := diagox.NewPBX(envConf, conf)
	if err := pbx.Init(ctx, conf); err != nil {
		log.Error("Failed to init PBX", "error", err)
		return
	}
	if err := pbx.Run(ctx); err != nil {
		log.Error("PBX finished with error", "error", err)
	}
}
