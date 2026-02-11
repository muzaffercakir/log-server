package db

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"log-server/config"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	mongoHost      = "cluster0.xp8w8mf.mongodb.net"
)

var (
	client     *mongo.Client
	once       sync.Once
	collection *mongo.Collection
)

// Connect MongoDB'ye bağlanır (singleton)
func Connect() error {
	var connErr error
	once.Do(func() {
		cfg := config.Get()

		// URI oluştur
		encodedPass := url.QueryEscape(cfg.DB.Password)
		uri := fmt.Sprintf("mongodb+srv://%s:%s@%s/?appName=Cluster0",
			cfg.DB.Username, encodedPass, mongoHost)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		clientOpts := options.Client().ApplyURI(uri)
		c, err := mongo.Connect(ctx, clientOpts)
		if err != nil {
			connErr = fmt.Errorf("MongoDB bağlantı hatası: %v", err)
			return
		}

		// Ping test
		if err := c.Ping(ctx, nil); err != nil {
			connErr = fmt.Errorf("MongoDB ping hatası: %v", err)
			return
		}


		client = c
		collection = client.Database(cfg.DB.DBName).Collection(cfg.DB.CollectionName)
		slog.Info("MongoDB bağlantısı başarılı", "db", cfg.DB.DBName, "collection", cfg.DB.CollectionName)
	})
	return connErr
}

// GetCollection logs koleksiyonunu döndürür
func GetCollection() *mongo.Collection {
	return collection
}

// InsertMany birden fazla dokümanı koleksiyona ekler
func InsertMany(ctx context.Context, docs []interface{}) error {
	if collection == nil {
		return fmt.Errorf("MongoDB koleksiyonu başlatılmamış")
	}

	result, err := collection.InsertMany(ctx, docs)
	if err != nil {
		return fmt.Errorf("MongoDB insert hatası: %v", err)
	}

	slog.Info("MongoDB'ye kayıt eklendi", "count", len(result.InsertedIDs))
	return nil
}

// Disconnect MongoDB bağlantısını kapatır
func Disconnect() {
	if client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Disconnect(ctx); err != nil {
			slog.Error("MongoDB disconnect hatası", "error", err)
		}
	}
}
