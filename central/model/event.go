// central/model/event.go
package model

type EventRecord struct {
	ClusterName string `bson:"cluster_name" json:"cluster_name"`
	Timestamp   int64  `bson:"timestamp" json:"timestamp"`
	Type        string `bson:"type" json:"type"`       // scale_up / scale_down
	Detail      string `bson:"detail" json:"detail"`
	EventID     string `bson:"event_id" json:"event_id"`
}