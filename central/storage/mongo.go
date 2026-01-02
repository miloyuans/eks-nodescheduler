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

			// raw_reports collection 用于存储源数据
			rawColl := db.Collection("raw_reports")
			ttlSeconds := int32(cfg.Mongo.TTLDays * 24 * 3600)
			rawIndex := mongo.IndexModel{
				Keys: bson.D{{Key: "createdAt", Value: 1}},
				Options: options.Index().
					SetName("ttl_raw_createdAt").
					SetExpireAfterSeconds(ttlSeconds),
			}
			if _, err := rawColl.Indexes().CreateOne(context.Background(), rawIndex); err != nil {
				log.Printf("[WARN] Create TTL index failed for raw_reports in %s: %v", cluster.Name, err)
			}

			// events collection 用于存储触发的事件（去重 + 查询）
			eventColl := db.Collection("events")
			eventIndex := mongo.IndexModel{
				Keys: bson.D{{Key: "eventID", Value: 1}},
				Options: options.Index().
					SetName("unique_event_id").
					SetUnique(true),
			}
			if _, err := eventColl.Indexes().CreateOne(context.Background(), eventIndex); err != nil {
				log.Printf("[WARN] Create unique index failed for events in %s: %v", cluster.Name, err)
			}

			clients[cluster.Name] = client
			log.Printf("[INFO] MongoDB initialized for cluster: %s (raw_reports + events collections)", cluster.Name)
		}
	}
	return nil
}

// StoreRawReport 存储源上报数据
func StoreRawReport(clusterName string, req model.ReportRequest) error {
	client, ok := clients[clusterName]
	if !ok {
		return fmt.Errorf("no mongo client for cluster %s", clusterName)
	}

	db := client.Database(clusterName)
	coll := db.Collection("raw_reports")

	type storedRaw struct {
		model.ReportRequest
		CreatedAt time.Time `bson:"createdAt"`
	}

	report := storedRaw{
		ReportRequest: req,
		CreatedAt:     time.Now(),
	}

	_, err := coll.InsertOne(context.Background(), report)
	if err != nil {
		return fmt.Errorf("store raw report failed for %s: %w", clusterName, err)
	}
	log.Printf("[INFO] Raw report stored for %s", clusterName)
	return nil
}

// EventExists 检查事件是否已存在（去重）
func EventExists(clusterName string, eventID string) bool {
	client, ok := clients[clusterName]
	if !ok {
		return false
	}

	db := client.Database(clusterName)
	coll := db.Collection("events")

	filter := bson.D{{Key: "eventID", Value: eventID}}
	count, err := coll.CountDocuments(context.Background(), filter)
	if err != nil {
		log.Printf("[WARN] Check event exists failed for %s (ID: %s): %v", clusterName, eventID, err)
		return false
	}
	return count > 0
}

// StoreEvent 存储触发的事件（已去重）
func StoreEvent(clusterName string, event model.EventRecord) error {
	client, ok := clients[clusterName]
	if !ok {
		return fmt.Errorf("no mongo client for cluster %s", clusterName)
	}

	db := client.Database(clusterName)
	coll := db.Collection("events")

	_, err := coll.InsertOne(context.Background(), event)
	if err != nil {
		return fmt.Errorf("store event failed for %s: %w", clusterName, err)
	}
	log.Printf("[INFO] Event stored for %s (ID: %s, Type: %s)", clusterName, event.EventID, event.Type)
	return nil
}

// QueryAllClusterReports 查询所有集群的事件记录（用于 web 页面）
func QueryAllClusterReports(limit int64) map[string][]map[string]interface{} {
	result := make(map[string][]map[string]interface{})

	for clusterName, client := range clients {
		db := client.Database(clusterName)
		coll := db.Collection("events") // 查询事件表

		opts := options.Find().
			SetSort(bson.D{{Key: "timestamp", Value: -1}}).
			SetLimit(limit)

		cursor, err := coll.Find(context.Background(), bson.D{}, opts)
		if err != nil {
			log.Printf("[WARN] Query events failed for cluster %s: %v", clusterName, err)
			continue
		}

		var events []map[string]interface{}
		if err := cursor.All(context.Background(), &events); err != nil {
			log.Printf("[WARN] Decode events failed for cluster %s: %v", clusterName, err)
			continue
		}

		// 添加集群名字段
		for i := range events {
			events[i]["cluster_name"] = clusterName
		}

		result[clusterName] = events
	}

	return result
}

// Shutdown 优雅关闭所有连接
func Shutdown() {
	for name, client := range clients {
		if err := client.Disconnect(context.Background()); err != nil {
			log.Printf("[WARN] Mongo disconnect failed for %s: %v", name, err)
		} else {
			log.Printf("[INFO] Mongo disconnected for %s", name)
		}
	}
}