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

// InitMongo 为每个集群初始化独立的 MongoDB 数据库
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

// StoreReport 存储 Agent 上报的数据
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

// Shutdown 优雅关闭所有 Mongo 连接
func Shutdown() {
	for name, client := range clients {
		if err := client.Disconnect(context.Background()); err != nil {
			log.Printf("[WARN] Mongo disconnect failed for %s: %v", name, err)
		} else {
			log.Printf("[INFO] Mongo disconnected for %s", name)
		}
	}
}

// QueryAllClusterReports 查询所有集群的最新报告（用于监控面板）
func QueryAllClusterReports(limit int64) map[string][]map[string]interface{} {
	result := make(map[string][]map[string]interface{})

	for clusterName, client := range clients {
		db := client.Database(clusterName)
		coll := db.Collection("reports")

		opts := options.Find().
			SetSort(bson.D{{Key: "createdAt", Value: -1}}). // 最新在前
			SetLimit(limit)

		cursor, err := coll.Find(context.Background(), bson.D{}, opts)
		if err != nil {
			log.Printf("[WARN] Query reports failed for cluster %s: %v", clusterName, err)
			continue
		}

		var reports []map[string]interface{}
		if err := cursor.All(context.Background(), &reports); err != nil {
			log.Printf("[WARN] Decode reports failed for cluster %s: %v", clusterName, err)
			continue
		}

		// 为每条记录添加 cluster_name 字段，方便前端显示
		for i := range reports {
			reports[i]["cluster_name"] = clusterName
		}

		result[clusterName] = reports
	}

	return result
}