package logger

import (
	"io"
	"log/slog"
	"os"

	"log-server/config"

	"gopkg.in/natefinch/lumberjack.v2"
)

var Log *slog.Logger

func Init() {
	cfg := config.Get()

	logRotator := &lumberjack.Logger{
		Filename:   cfg.InternalLog.LogFile,
		MaxSize:    10,
		MaxBackups: 3,
		MaxAge:     28,
		Compress:   true,
	}

	Log = slog.New(slog.NewJSONHandler(io.MultiWriter(os.Stdout, logRotator), &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	slog.SetDefault(Log)
}

func Get() *slog.Logger {
	return Log
}
