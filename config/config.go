package config

import (
	"fmt"
	"log-server/crypto"
	"os"

	"github.com/spf13/viper"
)

// Şifrelenmiş config değerlerini çözmek için kullanılan sabit anahtar
const magicKey = "!n0h0m-53cr3t-K3y-L0g-53rv3r"

type Config struct {
	Server      ServerConfig      `mapstructure:"server"`
	Auth        AuthConfig        `mapstructure:"auth"`
	DB          DBConfig          `mapstructure:"db"`
	InternalLog InternalLogConfig `mapstructure:"internal_log"`
	KettasLog   KettasLogConfig   `mapstructure:"kettas_log"`
	AiService   AiServiceConfig   `mapstructure:"ai_service"`
}

type DBConfig struct {
	Enabled        bool   `mapstructure:"enabled"`
	IsCloud        bool   `mapstructure:"is_cloud"`
	Host           string `mapstructure:"host"`
	Username       string `mapstructure:"username"`
	Password       string `mapstructure:"password"`
	DBName         string `mapstructure:"db_name"`
	CollectionName string `mapstructure:"collection_name"`
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
	UploadDir   string       `mapstructure:"upload_dir"`
	LogsDir     string       `mapstructure:"logs_dir"`
	ZipPassword string       `mapstructure:"zip_password"`
	MaxFileSizeMB int64        `mapstructure:"max_file_size_mb"`
	MaxFolderSizeMB int64        `mapstructure:"max_folder_size_mb"`
	Backup      BackupConfig `mapstructure:"backup"`
}

type BackupConfig struct {
	Enabled          bool   `mapstructure:"enabled"`
	CheckIntervalMin int    `mapstructure:"check_interval_min"`
	BackupDir        string `mapstructure:"backup_dir"`
	MaxBackupSizeMB  int64  `mapstructure:"max_backup_size_mb"`
	RetentionDays    int    `mapstructure:"retention_days"`
	DailyArchiveTargetTime string `mapstructure:"daily_archive_target_time"`
}

type AiServiceConfig struct {
	Url      string `mapstructure:"url"`      // AI model servisinin base URL'i
	Endpoint string `mapstructure:"endpoint"` // Endpoint yolu (ör: /analyze)
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

	// ENC(...) ile şifrelenmiş değerleri çöz
	if err := decryptSecrets(); err != nil {
		fmt.Printf("Error decrypting secrets: %v\n", err)
		os.Exit(1)
	}
}

// decryptSecrets ENC(...) ile sarılı tüm config değerlerini çözer
func decryptSecrets() error {
	fields := []*string{
		&AppConfig.DB.Username,
		&AppConfig.DB.Password,
	}

	for _, field := range fields {
		decrypted, err := crypto.DecryptIfEncrypted(*field, magicKey)
		if err != nil {
			return fmt.Errorf("decrypt hatası [%s]: %v", *field, err)
		}
		*field = decrypted
	}

	return nil
}

func Get() *Config {
	return &AppConfig
}
