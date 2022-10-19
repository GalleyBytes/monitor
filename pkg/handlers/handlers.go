package handlers

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/isaaguilar/terraform-operator/monitor/pkg/models"
	"github.com/isaaguilar/terraform-operator/monitor/pkg/tfohttpclient"
	"github.com/isaaguilar/terraform-operator/monitor/pkg/util"
	"gorm.io/gorm"
)

type handler struct {
	DB *gorm.DB
}

func New(db *gorm.DB) handler {
	return handler{DB: db}
}

func (h handler) GetOrSetCluster(name string) models.Cluster {
	cluster := models.Cluster{
		Name: name,
	}
	result := h.DB.Where(cluster).FirstOrCreate(&cluster)
	if result.Error != nil {
		log.Panic(result.Error)
	}
	return cluster
}

func (h handler) GetOrSetTFOResource(uuid, namespace, name, currentGeneration string, cluster models.Cluster) models.TFOResource {
	// resources := []models.TFOResource{}
	resourceSpec, err := tfohttpclient.ResourceSpec()
	if err != nil {
		// Print err and continue with blank spec
		log.Printf("ERROR could not read the resource spec: %s", err.Error())
	}
	tfoResource := models.TFOResource{}
	result := h.DB.Where("uuid = ?", uuid).First(&tfoResource)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		tfoResource = models.TFOResource{
			UUID:              uuid,
			Namespace:         namespace,
			Name:              name,
			CurrentGeneration: currentGeneration,
			Cluster:           cluster,
		}

		createResult := h.DB.Create(&tfoResource)
		if createResult.Error != nil {
			log.Panic(createResult.Error)
		}

		tfoResourceSpec := models.TFOResourceSpec{
			TFOResourceUUID: uuid,
			Generation:      currentGeneration,
			ResourceSpec:    string(resourceSpec),
		}

		createResult = h.DB.Create(&tfoResourceSpec)
		if createResult.Error != nil {
			log.Panic(createResult.Error)
		}

		return tfoResource
	} else if result.Error != nil {
		log.Panic(result.Error)
	}
	if tfoResource.ClusterID != cluster.ID {
		foundCluster := models.Cluster{}
		h.DB.First(&foundCluster, tfoResource.ClusterID)
		log.Fatalf("Resource UUID is bound to cluster #%d:%s but found cluster defined as #%d:%s",
			foundCluster.ID, foundCluster.Name,
			cluster.ID, cluster.Name,
		)
	}

	// Apply logic based updates
	if tfoResource.CurrentGeneration != currentGeneration {
		// An updated resource means that the workflow will be started from the beginning
		tfoResource.CurrentGeneration = currentGeneration
		tfoResourceSpec := models.TFOResourceSpec{
			TFOResourceUUID: uuid,
			Generation:      currentGeneration,
			ResourceSpec:    string(resourceSpec),
		}

		createResult := h.DB.Create(&tfoResourceSpec)
		if createResult.Error != nil {
			log.Panic(createResult.Error)
		}
	}
	h.DB.Save(&tfoResource)
	return tfoResource
}

func (h handler) WriteAllLines(tfoResource models.TFOResource, taskType, generation string, rerun int, tfoTaskLogs []models.TFOTaskLog) {
	start := time.Now()
	if len(tfoTaskLogs) == 0 {
		return
	}

	foundTFOTaskLogs := []models.TFOTaskLog{}
	result := h.DB.Where(models.TFOTaskLog{
		TaskType:        taskType,
		Rerun:           rerun,
		TFOResourceUUID: tfoResource.UUID,
		Generation:      generation,
	}, "task_type", "rerun", "tfo_resource_uuid", "generation").Find(&foundTFOTaskLogs)
	if result.Error != nil {
		log.Panic(result.Error)
	}

	// logLinesAlreadySaved := []string{}
	savedIndicies := []string{}
	for _, initLog := range foundTFOTaskLogs {
		log.Printf("%s Already have %s", "func WriteAllLines", initLog.Message)
		savedIndicies = append(savedIndicies, initLog.LineNo)
		// logLinesAlreadySaved = append(logLinesAlreadySaved, initLog.BufferIndex)
	}

	linesToWrite := []models.TFOTaskLog{}
	for _, initLog := range tfoTaskLogs {
		if !util.ContainsString(savedIndicies, initLog.LineNo) {
			log.Printf("%s Will write %s", "func WriteAllLines", initLog.Message)
			linesToWrite = append(linesToWrite, initLog)
		}
	}

	if len(linesToWrite) > 0 {
		createResult := h.DB.Create(&linesToWrite)
		if createResult.Error != nil {
			log.Panic(createResult.Error)
		}
	}

	log.Printf("func WriteAllLines took %s", time.Since(start).String())
}

func (h handler) EventWriter(file string, tfoResource models.TFOResource, taskType, generation string, rerun int) {
	// Let's write any .out to the database

	f, err := os.Open(file)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	fileScanner := bufio.NewScanner(f)
	fileScanner.Split(bufio.ScanLines)

	i := 0
	lines := []models.TFOTaskLog{}
	for fileScanner.Scan() {
		i++
		lines = append(lines, models.TFOTaskLog{
			Message:     fileScanner.Text(),
			Generation:  generation,
			TFOResource: tfoResource,
			TaskType:    taskType,
			Rerun:       rerun,
			LineNo:      fmt.Sprintf("%d", i),
		})
	}

	h.WriteAllLines(tfoResource, taskType, generation, rerun, lines)
}

// ParseFile takes the file path on the filesystem to check that is it a log file and returns the
// taskType, rerun number, and generation string. Will also return false if it is not a log file
// or if any of the other three components failed to validate.
func ParseFile(file string) (bool, string, int, string) {
	if filepath.Ext(file) != ".out" {
		return false, "", 0, ""
	}

	dir, filename := filepath.Split(file)
	nameSlice := strings.Split(filename, ".")
	taskType := nameSlice[0]
	rerun := 0
	if len(nameSlice) > 2 {
		// Rerun denoted by <rerun> in <task>.<rerun>.out
		if r, err := strconv.Atoi(nameSlice[1]); err == nil {
			rerun = r
		}
	}

	generation := filepath.Base(dir)
	_, err := strconv.Atoi(generation)
	if err != nil {
		return false, "", 0, ""
	}

	return true, taskType, rerun, generation

}
