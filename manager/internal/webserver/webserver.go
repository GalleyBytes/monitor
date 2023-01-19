package webserver

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"

	tfv1alpha2 "github.com/isaaguilar/terraform-operator/pkg/apis/tf/v1alpha2"
	"github.com/mattbaird/jsonpatch"
	admission "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

var (
	runtimeScheme = runtime.NewScheme()
	codecFactory  = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecFactory.UniversalDeserializer()
	jsonPatchType = admission.PatchTypeJSONPatch
)

// add kind AdmissionReview in scheme
func init() {
	_ = admission.AddToScheme(runtimeScheme)
	_ = tfv1alpha2.AddToScheme(runtimeScheme)
}

type access struct {
	apiServiceHost string
	apiUsername    string
	apiPassword    string
}

func (a access) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	url := fmt.Sprintf("%s/login", a.apiServiceHost)
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: tr}

	jsonData, err := json.Marshal(map[string]interface{}{
		"user":     a.apiUsername,
		"password": a.apiPassword,
	})
	if err != nil {
		log.Panic(err)
	}

	request, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
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

	loginResponseData := struct {
		Data []string `json:"data"`
	}{}
	err = json.Unmarshal(responseBody, &loginResponseData)
	if err != nil {
		log.Panic(err)
	}

	fmt.Fprintf(w, `{"host": "%s", "token": "%s"}`, a.apiServiceHost, loginResponseData.Data[0])
}

type mutationHandler struct {
	clusterName               string
	monitorManagerServiceHost string
	escapeKey                 string
	image                     string
	imagePullPolicy           corev1.PullPolicy
}

func (m mutationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	admissionHandler(w, r, m.mutate)
}

func (m *mutationHandler) mutate(ar admission.AdmissionReview) *admission.AdmissionResponse {
	log.Printf("mutating terraforms")

	group := tfv1alpha2.SchemeGroupVersion.Group
	version := tfv1alpha2.SchemeGroupVersion.Version
	terraformResource := metav1.GroupVersionResource{Group: group, Version: version, Resource: "terraforms"}
	if ar.Request.Resource != terraformResource {
		log.Printf("expect resource to be %s", terraformResource)
		return nil
	}
	raw := ar.Request.Object.Raw
	terraform := tfv1alpha2.Terraform{}

	if _, _, err := deserializer.Decode(raw, nil, &terraform); err != nil {
		log.Println(err)
		return &admission.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	// After the decode process, check if the resource needs mutations
	if terraform.ObjectMeta.Annotations == nil {
		terraform.ObjectMeta.Annotations = make(map[string]string)
	} else if _, found := terraform.ObjectMeta.Annotations[m.escapeKey]; found {
		// An escape route from mutating based on annotations. Or configure via mwc resource
		return nilPatch()
	}

	if terraform.Spec.Plugins == nil {
		terraform.Spec.Plugins = make(map[tfv1alpha2.TaskName]tfv1alpha2.Plugin)
	} else {
		for key := range terraform.Spec.Plugins {
			if key == "monitor" {
				log.Print("Overwriting existing monitor plugin")
				// return nilPatch()
			}
		}
	}

	terraform.Spec.Plugins["monitor"] = tfv1alpha2.Plugin{
		ImageConfig: tfv1alpha2.ImageConfig{
			Image:           m.image,
			ImagePullPolicy: m.imagePullPolicy,
		},
		Task: tfv1alpha2.RunSetup,
		When: "After",
	}

	if terraform.Spec.TaskOptions == nil {
		terraform.Spec.TaskOptions = []tfv1alpha2.TaskOption{}
	}
	monitorIndex := -1
	for i, taskOption := range terraform.Spec.TaskOptions {
		// Check for the existence of monitor to handle both cases
		if len(taskOption.For) == 1 {
			if taskOption.For[0] == "monitor" {
				log.Println("Found taskOptions for monitor")
				monitorIndex = i
			}
		}
	}

	configMapKeyMap := map[string]string{
		"CLUSTER_NAME":                 m.clusterName,
		"MONITOR_MANAGER_SERVICE_HOST": m.monitorManagerServiceHost,
	}

	if monitorIndex > -1 {
		// Monitor exists, now check the envs to update
		for i, env := range terraform.Spec.TaskOptions[monitorIndex].Env {
			if val, found := configMapKeyMap[env.Name]; found {
				log.Println("Found env", env.Name, "in index", i, "of monitor taskOption")
				terraform.Spec.TaskOptions[monitorIndex].Env[i] = corev1.EnvVar{
					Name:  env.Name,
					Value: val,
				}
				delete(configMapKeyMap, env.Name)
			}
			// j := slices.Index(configMapKeys, env.Name)
			// if j > -1 {
			// 	// Gather the indicies of this env in the configMapKeys. Indices will be used to remove keys from the
			// 	// list of keys that still need to be created
			// }

		}

		// Generate new envs from the remaining keys
		envs := []corev1.EnvVar{}

		for key, val := range configMapKeyMap {
			log.Println("Adding new env", key, "to envs of monitor taskOption")
			envs = append(envs, corev1.EnvVar{
				Name:  key,
				Value: val,
			})
		}

		terraform.Spec.TaskOptions[monitorIndex].Env = append(terraform.Spec.TaskOptions[monitorIndex].Env, envs...)
		terraform.Spec.TaskOptions[monitorIndex].RestartPolicy = corev1.RestartPolicyAlways
	} else {
		envs := []corev1.EnvVar{}

		for key, val := range configMapKeyMap {
			log.Println("Adding new env", key, "to envs of monitor taskOption")
			envs = append(envs, corev1.EnvVar{
				Name:  key,
				Value: val,
			})
		}
		terraform.Spec.TaskOptions = append(terraform.Spec.TaskOptions, tfv1alpha2.TaskOption{
			For:           []tfv1alpha2.TaskName{"monitor"},
			Env:           envs,
			RestartPolicy: corev1.RestartPolicyAlways,
		})
	}

	targetJson, err := json.Marshal(terraform)
	if err != nil {
		return &admission.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	patch, err := jsonpatch.CreatePatch(raw, targetJson)
	if err != nil {
		return &admission.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	terraformPatch, err := json.Marshal(patch)
	if err != nil {
		return &admission.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}
	for _, p := range patch {
		log.Println(p)
	}
	return &admission.AdmissionResponse{Allowed: true, PatchType: &jsonPatchType, Patch: terraformPatch}
}

// admissionHandler handles the http portion of a request prior to handing to an admissionFunc function
func admissionHandler(w http.ResponseWriter, r *http.Request, admissionFunc func(admission.AdmissionReview) *admission.AdmissionResponse) {
	var body []byte
	if r.Body != nil {
		if data, err := ioutil.ReadAll(r.Body); err == nil {
			body = data
		}
	}

	// verify the content type is accurate
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		log.Printf("contentType=%s, expect application/json", contentType)
		return
	}

	log.Printf("handling request: %s", body)
	var responseObj runtime.Object
	obj, gvk, err := deserializer.Decode(body, nil, nil)
	if err != nil {
		msg := fmt.Sprintf("Request could not be decoded: %v", err)
		log.Println(msg)
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	requestedAdmissionReview, ok := obj.(*admission.AdmissionReview)
	if !ok {
		log.Printf("Expected v1.AdmissionReview but got: %T", obj)
		return
	}

	responseAdmissionReview := &admission.AdmissionReview{}
	responseAdmissionReview.SetGroupVersionKind(*gvk)
	responseAdmissionReview.Response = admissionFunc(*requestedAdmissionReview)
	responseAdmissionReview.Response.UID = requestedAdmissionReview.Request.UID
	responseObj = responseAdmissionReview

	log.Printf("sending response: %v", responseObj)
	respBytes, err := json.Marshal(responseObj)
	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(respBytes); err != nil {
		log.Println(err)
	}
}

// Return an empty patch to satisfy the response
func nilPatch() *admission.AdmissionResponse {
	return &admission.AdmissionResponse{Allowed: true, PatchType: &jsonPatchType, Patch: []byte("[]")}
}

// Run starts the webserver and blocks
func Run(tlsCertFilename, tlsKeyFilename, image, escapeKey, clusterName, monitorManagerServiceHost string, imagePullPolicy corev1.PullPolicy, apiServiceHost, apiUsername, apiPassword string) {
	server := http.NewServeMux()
	server.Handle("/mutate", mutationHandler{
		clusterName:               clusterName,
		monitorManagerServiceHost: monitorManagerServiceHost,
		escapeKey:                 escapeKey,
		image:                     image,
		imagePullPolicy:           imagePullPolicy,
	})
	server.Handle("/api-token-please", access{
		apiServiceHost: apiServiceHost,
		apiUsername:    apiUsername,
		apiPassword:    apiPassword,
	})
	log.Printf("Server started ...")
	err := http.ListenAndServeTLS(":8443", tlsCertFilename, tlsKeyFilename, server)
	log.Fatal(err)
}
