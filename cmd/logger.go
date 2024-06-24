// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package cmd

import (
	"log/slog"
	"os"
	"time"

	"github.com/rs/zerolog"
	slogzerolog "github.com/samber/slog-zerolog"
)

func InitLogger() {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(os.Getenv("LOG_LEVEL"))); err != nil {
		lvl = slog.LevelInfo
	}
	slog.SetLogLoggerLevel(lvl)

	if f := os.Getenv("LOG_FORMAT"); f != "json" {
		zerologLogger := zerolog.New(zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: time.StampMicro,
		}).With().Timestamp().Logger()
		log := slog.New(slogzerolog.Option{Level: lvl, Logger: &zerologLogger}.NewZerologHandler())
		slog.SetDefault(log)
	} else {
		zerologLogger := zerolog.New(os.Stdout).With().Timestamp().Logger()
		log := slog.New(slogzerolog.Option{Level: lvl, Logger: &zerologLogger}.NewZerologHandler())
		slog.SetDefault(log)
	}
}
