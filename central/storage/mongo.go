// central/storage/mongo.go
package storage

import (
	"context"
	"log"

	"central/config"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var Clients = make(map[string]*mongo.Client) // key: cluster.Name

func InitMongo(cfg *config.GlobalConfig) error {
	for _, acct := range cfg.Accounts {
		for _, cluster := range acct.Clusters {
			client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(cfg.Mongo.URI))
			if err != nil {
				return err
			}
			db := client.Database(cluster.Name)
			coll := db.Collection("reports")

			ttlSec := int32(cfg.Mongo.TTLDays * 86400)
			index := mongo.IndexModel{
				Keys:    bson.D{{"createdAt", 1}},
				Options: options.Index().SetExpireAfterSeconds(ttlSec),
			}
			if _, err := coll.Indexes().CreateOne(context.Background(), index); err != nil {
				log.Printf("Create TTL for %s failed: %v", cluster.Name, err)
			}

			Clients[cluster.Name] = client
		}
	}
	return nil
}