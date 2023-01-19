package handlers

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/galleybytes/monitor/pkg/tfohttpclient"
	"github.com/galleybytes/monitor/pkg/util"
	"github.com/galleybytes/terraform-operator-api/pkg/api"
	"github.com/galleybytes/terraform-operator-api/pkg/common/models"
	gocache "github.com/patrickmn/go-cache"
)

func boolp(b bool) *bool {
	return &b
}

type handler struct {
	client *http.Client
	host   string
	token  string
	cache  *gocache.Cache
}

func New(url string, cache *gocache.Cache) handler {
	host, token := GetAPIAccess(url)
	return handler{
		client: &http.Client{},
		host:   host,
		token:  token,
		cache:  cache,
	}
}

func (h handler) doRequest(request *http.Request, fn func(interface{}) (interface{}, error)) (interface{}, *bool, error, error) {
	request.Header.Set("Token", h.token)
	request.Header.Set("Content-Type", "application/json; charset=UTF-8")
	response, err := h.client.Do(request)
	if err != nil {
		return nil, nil, nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return nil, nil, nil, nil
	}

	var data interface{}
	var hasData bool = true
	var status200 bool = true
	var errMsg string
	if response.StatusCode != 200 {
		status200 = false
		errMsg += fmt.Sprintf("request to %s returned a %d", request.URL, response.StatusCode)
	}

	responseBody, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, nil, nil, err
	}

	structuredResponse := api.Response{}
	err = json.Unmarshal(responseBody, &structuredResponse)
	if err != nil {
		return nil, nil, nil, err
	}

	if !status200 {
		errMsg += fmt.Sprintf(": %s", structuredResponse.StatusInfo.Message)
	}

	switch t := structuredResponse.Data.(type) {
	case []interface{}:
		if status200 {
			filteredData, err := fn(structuredResponse.Data.([]interface{}))
			if err != nil {
				errMsg = err.Error()
			}
			if filteredData == nil {
				hasData = false
			}
			data = filteredData
		}
	default:
		if status200 {
			hasData = false
			errMsg = fmt.Sprintf("response data expected as a list but got %T", t)
		}
	}

	return data, boolp(status200 && hasData), fmt.Errorf(errMsg), nil

}

func fnClusterResponse(arr interface{}) (interface{}, error) {
	i := arr.([]interface{})
	if len(i) == 0 {
		return nil, fmt.Errorf("did not contain data")
	}
	b, err := json.Marshal(i[0])
	if err != nil {
		return nil, err
	}
	var cluster models.Cluster
	err = json.Unmarshal(b, &cluster)
	if err != nil {
		return nil, err
	}
	return cluster, nil
}

func fnTFOResourceResponse(arr interface{}) (interface{}, error) {
	i := arr.([]interface{})
	if len(i) == 0 {
		return nil, fmt.Errorf("did not contain data")
	}
	b, err := json.Marshal(i[0])
	if err != nil {
		return nil, err
	}

	var obj models.TFOResource
	err = json.Unmarshal(b, &obj)
	if err != nil {
		return nil, err
	}
	return obj, nil
}

func fnTaskPodResponse(arr interface{}) (interface{}, error) {
	i := arr.([]interface{})
	if len(i) == 0 {
		return nil, fmt.Errorf("did not contain data")
	}
	b, err := json.Marshal(i[0])
	if err != nil {
		return nil, err
	}

	var obj models.TaskPod
	err = json.Unmarshal(b, &obj)
	if err != nil {
		return nil, err
	}
	return obj, nil
}

func fnTFOTaskLogs(arr interface{}) (interface{}, error) {
	i := arr.([]interface{})
	b, err := json.Marshal(i)
	if err != nil {
		return nil, err
	}

	var obj []models.TFOTaskLog
	err = json.Unmarshal(b, &obj)
	if err != nil {
		return nil, err
	}
	return obj, nil
}

func fnApprovalResponse(arr interface{}) (interface{}, error) {
	i := arr.([]interface{})
	if len(i) == 0 {
		return nil, fmt.Errorf("did not contain data")
	}
	b, err := json.Marshal(i[0])
	if err != nil {
		return nil, err
	}
	var approval models.Approval
	err = json.Unmarshal(b, &approval)
	if err != nil {
		return nil, err
	}
	return approval, nil
}

func fnAnyContent(n interface{}) (interface{}, error) {
	return new(interface{}), nil
}

// findClusterByID find the cluster by id. Returns the cluster model if found.
func (h handler) findClusterByID(id uint) (interface{}, *bool, error, error) {
	url := fmt.Sprintf("%s/api/v1/cluster/%d", h.host, id)
	request, err := http.NewRequest("GET", url, bytes.NewBuffer([]byte{}))
	if err != nil {
		return nil, nil, nil, err
	}

	return h.doRequest(request, fnClusterResponse)
}

// findCluster find the cluster by name. Returns the cluster model if found.
func (h handler) findCluster(name string) (interface{}, *bool, error, error) {
	url := fmt.Sprintf("%s/api/v1/cluster-name/%s", h.host, name)
	request, err := http.NewRequest("GET", url, bytes.NewBuffer([]byte{}))
	if err != nil {
		return nil, nil, nil, err
	}

	return h.doRequest(request, fnClusterResponse)

}

// addCluster registers a new cluster and returns the new cluster model
func (h handler) addCluster(name string) (interface{}, *bool, error, error) {
	jsonData, err := json.Marshal(map[string]interface{}{
		"cluster_name": name,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	url := fmt.Sprintf("%s/api/v1/cluster", h.host)
	request, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, nil, nil, err
	}

	return h.doRequest(request, fnClusterResponse)
}

// GetOrSetCluster will find an existing cluster or create a new one in the db.
// In any event where the cluster fails to be found or created, the monitor will panic.
func (h handler) GetOrSetCluster(name string) *models.Cluster {
	cluster := models.Cluster{}
	untypedCluster, found, _, err := h.findCluster(name)
	if untypedCluster != nil {
		cluster = untypedCluster.(models.Cluster)
	}
	if err != nil {
		log.Printf("Finding cluster '%s' failed", name)
		panic(err)
	}
	if found == nil {
		log.Panic("Type 'bool' was expected but got 'nil'")
	}
	if !*found {
		untypedNewCluster, found, reason, err := h.addCluster(name)
		if err != nil {
			log.Printf("Adding cluster '%s' failed", name)
			panic(err)
		}
		if found == nil {
			log.Panic("Type 'bool' was expected but got 'nil'")
		}
		if !*found {
			log.Panic(reason)
		}
		if untypedNewCluster == nil {
			log.Panic("expected a cluster type but found something else...fix me")
		}
		cluster = untypedNewCluster.(models.Cluster)

	}

	return &cluster
}

// GetOrSetTFOResource finds or updates the tfo_resource table in the database. The tfo_resource_spec is also
// updated in this call.
//
// TODO The "Create" part of this function is probably not a good place to create the resource. There is a
// chance that the monitor fails to start. In that case, the tfo_resource or the tfo_resource_spec is never
// created nor updated.
// The soltuion is to let the "monitor manager", a project that has the responsibility of modifying the
// "tf" kubernetes spec, be in charge of managing the tfo_resource and tfo_resource_spec to the database.
func (h handler) GetOrSetTFOResource(uuid, namespace, name, currentGeneration string, cluster models.Cluster) models.TFOResource {
	// resources := []models.TFOResource{}
	resourceSpec, err := tfohttpclient.ResourceSpec()
	if err != nil {
		// Print err and continue with blank spec
		log.Printf("ERROR could not read the resource spec: %s", err.Error())
	}

	url := fmt.Sprintf("%s/api/v1/resource/%s", h.host, uuid)
	request, err := http.NewRequest("GET", url, bytes.NewBuffer([]byte{}))
	if err != nil {
		log.Panic(err)
	}

	untypedTFOResource, found, _, err := h.doRequest(request, fnTFOResourceResponse)
	if err != nil {
		log.Panic(err)
	}
	if found == nil {
		log.Panic("Type 'bool' was expected but got 'nil'")
	}
	if !*found {
		// The TFOResource is not found so a new one has to be created.

		jsonData, err := json.Marshal(map[string]interface{}{
			"tfo_resource": models.TFOResource{
				UUID:              uuid,
				Namespace:         namespace,
				Name:              name,
				CurrentGeneration: currentGeneration,
				Cluster:           cluster,
			},
		})
		if err != nil {
			log.Panic(err)
		}
		url := fmt.Sprintf("%s/api/v1/resource", h.host)
		request, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
		if err != nil {
			log.Panic(err)
		}

		untypedTFOResource, found, reason, err := h.doRequest(request, fnTFOResourceResponse)
		if err != nil {
			log.Panic(err)
		}
		if found == nil {
			log.Panic("Type 'bool' was expected but got 'nil'")
		}
		if !*found {
			log.Panic(reason)
		}

		jsonData, err = json.Marshal(map[string]interface{}{
			"tfo_resource_spec": models.TFOResourceSpec{
				TFOResourceUUID: uuid,
				Generation:      currentGeneration,
				ResourceSpec:    string(resourceSpec),
			},
		})
		if err != nil {
			log.Panic(err)
		}
		url = fmt.Sprintf("%s/api/v1/resource-spec", h.host)
		request, err = http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
		if err != nil {
			log.Panic(err)
		}

		_, found, reason, err = h.doRequest(request, fnAnyContent)
		if err != nil {
			log.Panic(err)
		}
		if found == nil {
			log.Panic("Type 'bool' was expected but got 'nil'")
		}
		if !*found {
			log.Panic(reason)
		}

		return untypedTFOResource.(models.TFOResource)
	}

	// The TFOResource was found in the database. First do a quick sanity check of the clusterID that the
	// TFOResource stored in the database has.
	tfoResource := untypedTFOResource.(models.TFOResource)
	if tfoResource.ClusterID != cluster.ID {
		//TODO The clusterID was detected to not match. What should happen if the clusterID is different?

		// TODO This code resolves the cluster to get the name of the cluster. The name is used only for a error message.
		// The query to resolve the name should be removed. The user can query the api with the given cluster id if they
		// need more information.
		foundCluster := models.Cluster{}
		untypedCluster, found, reason, err := h.findClusterByID(tfoResource.ClusterID)
		if err != nil {
			log.Panic(err)
		}
		if found == nil {
			log.Panic("Type 'bool' was expected but got 'nil'")
		}
		if !*found {
			log.Panic(reason)
		}
		if untypedCluster == nil {
			log.Panic("expected a cluster type but found something else...fix me")
		}
		foundCluster = untypedCluster.(models.Cluster)
		log.Fatalf("Resource UUID is bound to cluster #%d:%s but found cluster defined as #%d:%s",
			foundCluster.ID, foundCluster.Name,
			cluster.ID, cluster.Name,
		)
	}

	if tfoResource.CurrentGeneration != currentGeneration {
		tfoResource.CurrentGeneration = currentGeneration

		jsonData, err := json.Marshal(map[string]interface{}{
			"tfo_resource_spec": models.TFOResourceSpec{
				TFOResourceUUID: uuid,
				Generation:      currentGeneration,
				ResourceSpec:    string(resourceSpec),
			},
		})
		if err != nil {
			log.Panic(err)
		}
		url = fmt.Sprintf("%s/api/v1/resource-spec", h.host)
		request, err = http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
		if err != nil {
			log.Panic(err)
		}

		_, found, reason, err := h.doRequest(request, fnAnyContent)
		if err != nil {
			log.Panic(err)
		}
		if found == nil {
			log.Panic("Type 'bool' was expected but got 'nil'")
		}
		if !*found {
			log.Panic(reason)
		}
	}

	jsonData, err := json.Marshal(map[string]interface{}{
		"tfo_resource": tfoResource,
	})
	if err != nil {
		log.Panic(err)
	}
	url = fmt.Sprintf("%s/api/v1/resource", h.host)
	request, err = http.NewRequest("PUT", url, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Panic(err)
	}

	untypedTFOResource, found, reason, err := h.doRequest(request, fnTFOResourceResponse)
	if err != nil {
		log.Panic(err)
	}
	if found == nil {
		log.Panic("Type 'bool' was expected but got 'nil'")
	}
	if !*found {
		log.Panic(reason)
	}
	return untypedTFOResource.(models.TFOResource)
}

// WriteAllLines compares logs-to-write with logs-already-written (in the database) to check if the LINENO exists.
// It does not check the contents of the line. It prunes the lines that already have been written based on LINENO.
// After determining what lines to write, it sends the logs to get saved to the database.
//
// Failures to communicate with the database will cause a panic.
func (h handler) WriteAllLines(tfoResource models.TFOResource, taskPod models.TaskPod, tfoTaskLogs []models.TFOTaskLog) {
	start := time.Now()
	if len(tfoTaskLogs) == 0 {
		return
	}

	url := fmt.Sprintf("%s/api/v1/task/%s/logs", h.host, taskPod.UUID)
	request, err := http.NewRequest("GET", url, bytes.NewBuffer([]byte{}))
	if err != nil {
		log.Panic(err)
	}

	untypedTFOTaskLogs, found, _, err := h.doRequest(request, fnTFOTaskLogs)
	if err != nil {
		log.Panic(err)
	}
	if found == nil {
		log.Panic("Type 'bool' was expected but got 'nil'")
	}
	foundTFOTaskLogs := untypedTFOTaskLogs.([]models.TFOTaskLog)
	savedIndicies := []string{}
	for _, initLog := range foundTFOTaskLogs {
		// log.Printf("%s Already have %s", "func WriteAllLines", initLog.Message)
		savedIndicies = append(savedIndicies, initLog.LineNo)
	}

	linesToWrite := []models.TFOTaskLog{}
	for _, initLog := range tfoTaskLogs {
		if !util.ContainsString(savedIndicies, initLog.LineNo) {
			// log.Printf("%s Will write %s", "func WriteAllLines", initLog.Message)
			linesToWrite = append(linesToWrite, initLog)
		}
	}

	if len(linesToWrite) > 0 {

		jsonData, err := json.Marshal(map[string]interface{}{
			"tfo_task_logs": linesToWrite,
		})
		if err != nil {
			log.Panic(err)
		}
		url := fmt.Sprintf("%s/api/v1/logs", h.host)
		request, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
		if err != nil {
			log.Panic(err)
		}

		_, _, reason, err := h.doRequest(request, fnAnyContent)
		if err != nil {
			log.Panic(err)
		}
		if reason != nil {
			log.Panic(reason)
		}
	}

	log.Printf("Wrote %d lines in %s", len(linesToWrite), time.Since(start).String())
}

func (h handler) EventWriter(file string, tfoResource models.TFOResource, taskType, generation string, rerun int, uid string) {
	// Let's write any .out to the database

	taskPod := models.TaskPod{}
	if cached, found := h.cache.Get(uid); found {
		taskPod = cached.(models.TaskPod)
	} else {
		jsonData, err := json.Marshal(map[string]interface{}{
			"task_pod": models.TaskPod{
				UUID:        uid,
				Rerun:       rerun,
				Generation:  generation,
				TaskType:    taskType,
				TFOResource: tfoResource,
			},
		})
		if err != nil {
			log.Panic(err)
		}
		url := fmt.Sprintf("%s/api/v1/task", h.host)
		request, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
		if err != nil {
			log.Panic(err)
		}

		untypedTaskPod, found, reason, err := h.doRequest(request, fnTaskPodResponse)
		if err != nil {
			log.Panicf("error handling request for task with uid '%s' of type '%s': %s", uid, taskType, err)
		}
		if found == nil {
			log.Panic("Type 'bool' was expected but got 'nil'")
		}
		if !*found {
			log.Panicf("error handling request for task with uid '%s' of type '%s': %s", uid, taskType, reason)

		}
		taskPod = untypedTaskPod.(models.TaskPod)
		h.cache.Set(uid, taskPod, gocache.NoExpiration)
	}

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
			TFOResource: tfoResource,
			TaskPod:     taskPod,
			LineNo:      fmt.Sprintf("%d", i),
		})
	}

	h.WriteAllLines(tfoResource, taskPod, lines)
}

// FindApprovals checks logs by the task_log_uuid for approval statuses in the database. When approved, a file
// is created using a specific naming convention.
// See https://github.com/GalleyBytes/terraform-operator-tasks/commit/7b2ab6813696def5ca806de9fe52b09a164de6fb
func (h handler) FindApprovals(uids []string, dir string) {

	approvals := []models.Approval{}
	for _, uid := range uids {
		url := fmt.Sprintf("%s/api/v1/task/%s/approval-status", h.host, uid)
		request, err := http.NewRequest("GET", url, bytes.NewBuffer([]byte{}))
		if err != nil {
			log.Panic(err)
		}

		approval, found, _, err := h.doRequest(request, fnApprovalResponse)
		if err != nil {
			log.Panic(err)
		}
		if found == nil {
			log.Panic("Type 'bool' was expected but got 'nil'")
		}
		if !*found {
			// The uid has not been registered, status is undefined. This is ok and is probably the most seen
			// case. Typically an undefined approval status means that an approval was not required or
			// is yet to be decided.
			// log.Println(m)
			continue
		}
		approvals = append(approvals, approval.(models.Approval))
	}

	result := struct{ Error error }{}
	if result.Error != nil {
		log.Println("Error fetching approvals")
	}
	for _, approval := range approvals {
		if approval.IsApproved {
			ioutil.WriteFile(fmt.Sprintf("%s/_approved_%s", dir, approval.TaskPodUUID), []byte{}, 0644)
		} else {
			ioutil.WriteFile(fmt.Sprintf("%s/_canceled_%s", dir, approval.TaskPodUUID), []byte{}, 0644)
		}
	}
}

// ParseFile takes the file path on the filesystem to check that is it a log file and returns the
// taskType, rerun number, and generation string. Will also return false if it is not a log file
// or if any of the other three components failed to validate.
func ParseFile(file string) (bool, string, int, string, string) {
	if filepath.Ext(file) != ".out" {
		return false, "", 0, "", ""
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

	uid := ""
	if len(nameSlice) > 3 {
		// Assuming this is the format <Task>.<rerun>.<uuid>.out
		uid = nameSlice[2]
	}

	generation := filepath.Base(dir)
	_, err := strconv.Atoi(generation)
	if err != nil {
		return false, "", 0, "", ""
	}

	return true, taskType, rerun, generation, uid

}

func GetAPIAccess(url string) (string, string) {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: tr}

	request, err := http.NewRequest("GET", url, bytes.NewBuffer([]byte{}))
	if err != nil {
		log.Panic(err)
	}

	request.Header.Set("Content-Type", "application/json; charset=UTF-8")
	response, err := client.Do(request)
	if err != nil {
		log.Panic(err)
	}
	defer response.Body.Close()

	if response.StatusCode != 200 {
		log.Panicf("Request to %s returned a %d but expected 200", request.URL, response.StatusCode)
	}

	responseBody, err := ioutil.ReadAll(response.Body)
	if err != nil {
		log.Panic(err)
	}

	accessResponseData := struct {
		Host  string `json:"host"`
		Token string `json:"token"`
	}{}
	err = json.Unmarshal(responseBody, &accessResponseData)
	if err != nil {
		log.Panic(err)
	}

	return accessResponseData.Host, accessResponseData.Token
}
