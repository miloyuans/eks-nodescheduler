// agent/model/report.go
package model

type ReportRequest struct {
	ClusterName string         `json:"cluster_name"`
	NodeGroups  []NodeGroupData `json:"node_groups"`
	Timestamp   int64          `json:"timestamp"`
}

type NodeGroupData struct {
	Name        string            `json:"name"`
	NodeUtils   map[string]float64 `json:"node_utils"`
	Nodes       []NodeInfo        `json:"nodes"`
}

type NodeInfo struct {
	Name                string `json:"name"`
	InstanceId          string `json:"instance_id"`
	RequestCpuMilli     int64  `json:"request_cpu_milli"`
	AllocatableCpuMilli int64  `json:"allocatable_cpu_milli"`
}