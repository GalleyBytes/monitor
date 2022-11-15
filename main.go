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
	"github.com/isaaguilar/terraform-operator/monitor/pkg/handlers"
	"github.com/isaaguilar/terraform-operator/monitor/pkg/models"
	gocache "github.com/patrickmn/go-cache"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var (
	watcher            *fsnotify.Watcher
	err                error
	clusterName        string
	resourceUUID       string
	resourceName       string
	resourceNamespace  string
	resourceGeneration string
	generationsDir     string
	pgpassword         string
	pguser             string
	pgdb               string
	pghost             string
	pgport             string
	env                string
)

func Init() *gorm.DB {

	env = os.Getenv("ENV")
	pgpassword = os.Getenv("PGPASSWORD")
	pguser = os.Getenv("PGUSER")
	pgdb = os.Getenv("PGDATABASE")
	pgport = os.Getenv("PGPORT")
	if pgport == "" {
		pgport = "5432"
	}
	pghost = os.Getenv("DBHOST")
	if pghost == "" {
		pghost = "localhost"
	}

	// There are two ways of using creds. The connection string and the dsn
	// dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=disable", pghost, pguser, pgpassword, pgdb, pgport)
	dbURL := fmt.Sprintf("postgres://%s:%s@%s:%s/%s", pguser, pgpassword, pghost, pgport, pgdb)

	db, err := gorm.Open(postgres.Open(dbURL), &gorm.Config{})
	if err != nil {
		log.Panic(err)
	}

	// Do not migrate when running this service. The owner of the models (which will not be this monitor)
	// will be responsible for database migrations.
	if env == "devlocal" {
		db.AutoMigrate(
			&models.TFOTaskLog{},
			&models.TFOResourceSpec{},
			&models.Approval{},
		)
	}
	return db
}

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
	db := Init()
	h := handlers.New(db, cache)
	cluster := h.GetOrSetCluster(clusterName)
	log.Print("Cluster is ", cluster.Name)
	tfoResource := h.GetOrSetTFOResource(resourceUUID, resourceNamespace, resourceName, resourceGeneration, cluster)
	log.Print("TFO Resource is ", tfoResource.Namespace, "/", tfoResource.Name, ", UUID:", tfoResource.UUID)
	watcher, err = fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

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
		h.EventWriter(file, tfoResource, taskType, generation, rerun, uid)
	}

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
					h.EventWriter(event.Name, tfoResource, taskType, generation, rerun, uid)
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
	for {
		items := cache.Items()
		uids := []string{}
		for key, _ := range items {
			uids = append(uids, key)
		}
		h.FindApprovals(uids, generationsDir)
		time.Sleep(15 * time.Second)
	}

	// // Wait until done is returned.
	// <-done
}
