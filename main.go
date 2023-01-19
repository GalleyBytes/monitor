package main

import (
	"fmt"
	"io/fs"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/galleybytes/monitor/pkg/handlers"
	gocache "github.com/patrickmn/go-cache"
)

var (
	err                error
	watcher            *fsnotify.Watcher
	clusterName        string
	resourceUUID       string
	resourceName       string
	resourceNamespace  string
	resourceGeneration string
	generationsDir     string
	managerServiceHost string
)

// addWatcher adds files under the generation dir
func addWatcher(fileInfo fs.FileInfo, path string) {
	file := filepath.Join(path, fileInfo.Name())
	if _, err := os.Stat(file); err == nil {
		if filepath.Ext(file) == ".out" {
			watcher.Add(file)
		}
	}
}

func init() {
	managerServiceHost = os.Getenv("MONITOR_MANAGER_SERVICE_HOST")
	if managerServiceHost == "" {
		log.Fatal("MONITOR_MANAGER_SERVICE_HOST cannot be empty")
	}

	clusterName = os.Getenv("CLUSTER_NAME")
	if clusterName == "" {
		log.Fatal("CLUSTER_NAME cannot be empty")
	}

	resourceUUID = os.Getenv("TFO_RESOURCE_UUID")
	if resourceUUID == "" {
		log.Fatal("TFO_RESOURCE_UUID cannot be empty")
	}

	resourceNamespace = os.Getenv("TFO_NAMESPACE")
	if resourceNamespace == "" {
		log.Fatal("TFO_NAMESPACE cannot be empty")
	}

	resourceName = os.Getenv("TFO_RESOURCE")
	if resourceName == "" {
		log.Fatal("TFO_RESOURCE cannot be empty")
	}

	resourceGeneration = os.Getenv("TFO_GENERATION")
	if resourceGeneration == "" {
		log.Fatal("TFO_GENERATION cannot be empty")
	}

	generationsRootDir := os.Getenv("TFO_ROOT_PATH")
	if generationsRootDir == "" {
		log.Fatal("TFO_ROOT_PATH cannot be empty")
	}
	generationsDir = fmt.Sprintf("%s/generations/%s", generationsRootDir, resourceGeneration)

}

func main() {
	// cancelChan := make(chan os.Signal, 1)
	// // catch SIGETRM or SIGINTERRUPT
	// signal.Notify(cancelChan, syscall.SIGTERM, syscall.SIGINT)
	// go func() {
	// 	for {
	// 		time.Sleep(time.Second)
	// 	}
	// }()
	cache := gocache.New(gocache.NoExpiration, gocache.NoExpiration)
	requestHandler := handlers.New(managerServiceHost+"/api-token-please", cache)

	cluster := requestHandler.GetOrSetCluster(clusterName)
	tfoResource := requestHandler.GetOrSetTFOResource(resourceUUID, resourceNamespace, resourceName, resourceGeneration, *cluster)
	log.Print("TFO Resource is ", tfoResource.Namespace, "/", tfoResource.Name, ", UUID:", tfoResource.UUID)
	watcher, err = fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	log.Print("Finding files")
	for {
		fileInfo, err := os.Stat(generationsDir)
		if err == nil {
			if fileInfo.IsDir() {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	watcher.Add(generationsDir)
	// List the directory of the generation which is where logs will be
	fileInfos, err := ioutil.ReadDir(generationsDir)
	if err != nil {
		log.Fatal(err)
	}

	// Read in all files on init
	for _, fileInfo := range fileInfos {
		file := filepath.Join(generationsDir, fileInfo.Name())
		isLog, taskType, rerun, generation, uid := handlers.ParseFile(file)
		if !isLog {
			continue
		}
		requestHandler.EventWriter(file, tfoResource, taskType, generation, rerun, uid)
	}
	log.Print("Starting log watcher")
	// done := make(chan bool)
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				log.Printf("event: '%s': %s", event.Name, event.Op)

				if event.Op == fsnotify.Create {
					file, err := os.Stat(event.Name)
					if err == nil {
						if file.IsDir() {
							// I don't think we need any more watchers?
							// addWatcher(file, generationsDir)
							continue
						}
					}
				}
				if event.Op == fsnotify.Create || event.Op == fsnotify.Write {
					isLog, taskType, rerun, generation, uid := handlers.ParseFile(event.Name)
					if !isLog {
						continue
					}
					requestHandler.EventWriter(event.Name, tfoResource, taskType, generation, rerun, uid)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("error:", err)

				// case sig := <-cancelChan:
				// 	log.Printf("Caught SIGTERM %v", sig)
				// 	done <- true
			}

		}
	}()

	// Start a poll for messages on the approvals model
	log.Println("Starting approval watcher")
	for {
		items := cache.Items()
		uids := []string{}
		for key, _ := range items {
			uids = append(uids, key)
		}
		requestHandler.FindApprovals(uids, generationsDir)
		time.Sleep(15 * time.Second)
	}

	// // Wait until done is returned.
	// <-done
}
