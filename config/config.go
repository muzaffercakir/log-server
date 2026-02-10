package config

import (
	"fmt"
	"os"

	"github.com/spf13/viper"
)

type Config struct {
	Server      ServerConfig      `mapstructure:"server"`
	Auth        AuthConfig        `mapstructure:"auth"`
	InternalLog InternalLogConfig `mapstructure:"internal_log"`
	KettasLog   KettasLogConfig   `mapstructure:"kettas_log"`
}

type AuthConfig struct {
	ApiKey   string `mapstructure:"api_key"`   // Header adı (ör: "inohom-api-key")
	ApiValue string `mapstructure:"api_value"` // API secret değeri
}

type ServerConfig struct {
	Port string `mapstructure:"port"`
}

type InternalLogConfig struct {
	LogFile string `mapstructure:"log_file"`
}

type KettasLogConfig struct {
	UploadDir   string `mapstructure:"upload_dir"`
	LogsDir     string `mapstructure:"logs_dir"`
	ZipPassword string `mapstructure:"zip_password"`
	MaxFileSize int64  `mapstructure:"max_file_size"`
}

var AppConfig Config

func Load() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")

	if err := viper.ReadInConfig(); err != nil {
		fmt.Printf("Error reading config file: %v\n", err)
		os.Exit(1)
	}

	if err := viper.Unmarshal(&AppConfig); err != nil {
		fmt.Printf("Error unmarshalling config: %v\n", err)
		os.Exit(1)
	}
}

func Get() *Config {
	return &AppConfig
}
