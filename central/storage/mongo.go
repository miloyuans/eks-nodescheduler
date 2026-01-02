// central/storage/mongo.go
package storage

import (
	"context"
	"fmt"
	"log"
	"time"

	"central/config"
	"central/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var clients map[string]*mongo.Client = make(map[string]*mongo.Client)

func InitMongo(cfg *config.GlobalConfig) error {
	for _, acct := range cfg.Accounts {
		for _, cluster := range acct.Clusters {
			clientOpts := options.Client().ApplyURI(cfg.Mongo.URI)
			client, err := mongo.Connect(context.Background(), clientOpts)
			if err != nil {
				return fmt.Errorf("mongo connect failed for cluster %s: %w", cluster.Name, err)
			}

			if err := client.Ping(context.Background(), nil); err != nil {
				return fmt.Errorf("mongo ping failed for cluster %s: %w", cluster.Name, err)
			}

			db := client.Database(cluster.Name)
			coll := db.Collection("reports")

			ttlSeconds := int32(cfg.Mongo.TTLDays * 24 * 3600)
			indexModel := mongo.IndexModel{
				Keys: bson.D{{Key: "createdAt", Value: 1}},
				Options: options.Index().
					SetName("ttl_createdAt").
					SetExpireAfterSeconds(ttlSeconds),
			}

			_, err = coll.Indexes().CreateOne(context.Background(), indexModel)
			if err != nil {
				log.Printf("[WARN] Create TTL index failed for %s: %v", cluster.Name, err)
			}

			clients[cluster.Name] = client
			log.Printf("[INFO] MongoDB initialized for cluster: %s", cluster.Name)
		}
	}
	return nil
}

func StoreReport(clusterName string, req model.ReportRequest) error {
	client, ok := clients[clusterName]
	if !ok {
		return fmt.Errorf("no mongo client for cluster %s", clusterName)
	}

	db := client.Database(clusterName)
	coll := db.Collection("reports")

	type storedReport struct {
		model.ReportRequest
		CreatedAt time.Time `bson:"createdAt"`
	}

	report := storedReport{
		ReportRequest: req,
		CreatedAt:     time.Now(),
	}

	_, err := coll.InsertOne(context.Background(), report)
	if err != nil {
		return fmt.Errorf("mongo insert failed for %s: %w", clusterName, err)
	}
	return nil
}

func Shutdown() {
	for name, client := range clients {
		if err := client.Disconnect(context.Background()); err != nil {
			log.Printf("[WARN] Mongo disconnect failed for %s: %v", name, err)
		} else {
			log.Printf("[INFO] Mongo disconnected for %s", name)
		}
	}
}

// QueryReports 查询所有集群的报告数据
func QueryReports() map[string][]map[string]interface{} {
	data := make(map[string][]map[string]interface{})

	for name, client := range clients {
		db := client.Database(name)
		coll := db.Collection("reports")

		opts := options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetLimit(50) // 最新 50 条

		cursor, err := coll.Find(context.Background(), bson.D{}, opts)
		if err != nil {
			log.Printf("[ERROR] Query reports failed for %s: %v", name, err)
			continue
		}

		var reports []map[string]interface{}
		if err := cursor.All(context.Background(), &reports); err != nil {
			log.Printf("[ERROR] Decode reports failed for %s: %v", name, err)
			continue
		}

		data[name] = reports
	}

	return data
}