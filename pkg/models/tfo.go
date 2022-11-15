package models

import (
	"time"

	"gorm.io/gorm"
)

type TFOTaskLog struct {
	gorm.Model
	TaskPod         TaskPod     `json:"task_pod,omitempty"`
	TaskPodUUID     string      `json:"task_pod_uuid"`
	TFOResource     TFOResource `json:"tfo_resource,omitempty"`
	TFOResourceUUID string      `json:"tfo_resource_uuid"`
	Message         string      `json:"message"`
	LineNo          string      `json:"lineNo"`
}

type TaskPod struct {
	UUID            string      `json:"uuid" gorm:"primaryKey"`
	TaskType        string      `json:"task_type"`
	Rerun           int         `json:"rerun"`
	Generation      string      `json:"generation"`
	TFOResource     TFOResource `json:"tfo_resource,omitempty"`
	TFOResourceUUID string      `json:"tfo_resource_uuid"`
}

type TFOResourceSpec struct {
	gorm.Model
	TFOResource     TFOResource
	TFOResourceUUID string `json:"tfo_resource_uuid"`
	Generation      string
	ResourceSpec    string
}

type TFOResource struct {
	UUID      string `json:"uuid" gorm:"primaryKey"`
	CreatedBy time.Time
	CreatedAt time.Time
	UpdatedBy time.Time
	UpdatedAt time.Time
	DeletedBy time.Time
	DeletedAt time.Time

	// NamespacedName comprises a resource name, with a mandatory namespace,
	// rendered as "<namespace>/<name>".
	Namespace string
	Name      string

	Cluster   Cluster
	ClusterID uint

	CurrentGeneration string
}

type Cluster struct {
	gorm.Model
	Name string
}

type Approval struct {
	gorm.Model
	IsApproved  bool    `json:"is_approved"`
	TaskPod     TaskPod `json:"task_pod,omitempty"`
	TaskPodUUID string  `json:"task_pod_uuid"`
}
