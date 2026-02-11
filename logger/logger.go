package logger

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"log-server/config"

	"gopkg.in/natefinch/lumberjack.v2"
)

var Log *slog.Logger

func Init() {
	cfg := config.Get()

	dir := filepath.Dir(cfg.InternalLog.LogFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		// Log klasörü oluşturulamazsa stdout'a yazıp devam edelim veya panic
		panic("Log dizini oluşturulamadı: " + err.Error())
	}

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
