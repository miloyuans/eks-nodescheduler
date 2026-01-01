// central/storage/mongo.go
package storage

import (
	"context"
	"fmt"
	"log"
	"time"

	"central/config"
	"central/proto"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var clients map[string]*mongo.Client = make(map[string]*mongo.Client)

// InitMongo 为每个 EKS 集群创建一个独立的 MongoDB Database，并创建 TTL 索引
func InitMongo(cfg *config.GlobalConfig) error {
	for _, acct := range cfg.Accounts {
		for _, cluster := range acct.Clusters {
			clientOpts := options.Client().ApplyURI(cfg.Mongo.URI)
			client, err := mongo.Connect(context.Background(), clientOpts)
			if err != nil {
				return fmt.Errorf("mongo connect failed for cluster %s: %w", cluster.Name, err)
			}

			// 测试连接
			if err := client.Ping(context.Background(), nil); err != nil {
				return fmt.Errorf("mongo ping failed for cluster %s: %w", cluster.Name, err)
			}

			db := client.Database(cluster.Name)
			coll := db.Collection("reports")

			// 创建 TTL 索引：根据 createdAt 字段自动过期
			ttlSeconds := int32(cfg.Mongo.TTLDays * 24 * 3600)
			indexModel := mongo.IndexModel{
				Keys: bson.D{{Key: "createdAt", Value: 1}},
				Options: options.Index().
					SetName("ttl_createdAt").
					SetExpireAfterSeconds(ttlSeconds),
			}

			_, err = coll.Indexes().CreateOne(context.Background(), indexModel)
			if err != nil {
				log.Printf("Warning: create TTL index failed for %s: %v", cluster.Name, err)
				// 不返回错误，继续运行
			}

			clients[cluster.Name] = client
			log.Printf("MongoDB initialized for cluster: %s", cluster.Name)
		}
	}
	return nil
}

// StoreReport 将 Agent 上报的数据持久化到对应集群的 MongoDB
func StoreReport(clusterName string, req *proto.ReportRequest) error {
	client, ok := clients[clusterName]
	if !ok {
		return fmt.Errorf("no mongo client for cluster %s", clusterName)
	}

	db := client.Database(clusterName)
	coll := db.Collection("reports")

	// 添加时间戳
	type storedReport struct {
		*proto.ReportRequest
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