// central/model/event.go
package model

type EventRecord struct {
	ClusterName string `bson:"cluster_name"`
	Timestamp   int64  `bson:"timestamp"`
	Type        string `bson:"type"` // scale_up / scale_down
	Detail      string `bson:"detail"`
	EventID     string `bson:"event_id"`
}