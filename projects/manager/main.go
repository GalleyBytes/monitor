package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	tfv1alpha2 "github.com/isaaguilar/terraform-operator/pkg/apis/tf/v1alpha2"
	"github.com/mattbaird/jsonpatch"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

func kubernetesConfig(kubeconfigPath string) *rest.Config {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		log.Fatal("Failed to get config for clientset")
	}
	return config
}

func envOrPanic(env string, defaultValue ...string) string {
	value := os.Getenv(env)
	if value == "" && len(defaultValue) == 0 {
		log.Panicf("%s is required", env)
	} else if value == "" {
		value = defaultValue[0]
	}
	return value
}

func createOrUpdateConfigMap(client kubernetes.Interface, resourceName, namespace string, data map[string]string, ownerReference metav1.OwnerReference) error {
	ctx := context.TODO()
	name := fmt.Sprintf("%s-monitor-envs", resourceName)
	configMapClient := client.CoreV1().ConfigMaps(namespace)

	configMap, err := configMapClient.Get(ctx, name, metav1.GetOptions{})
	if err != nil && errors.IsNotFound(err) {
		configMap := corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:            name,
				OwnerReferences: []metav1.OwnerReference{ownerReference},
			},
			Data: data,
		}
		_, err := configMapClient.Create(ctx, &configMap, metav1.CreateOptions{})
		if err != nil {
			return err
		}
		log.Printf("...created configmap '%s/%s'\n", namespace, name)
		// return nil
	} else if err != nil {
		return err
	} else {
		configMap.Data = data
		_, err = configMapClient.Update(ctx, configMap, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
		log.Printf("...updated configmap '%s/%s'\n", namespace, name)
		// return nil
	}
	return nil
}

func boolp(b bool) *bool {
	return &b
}

func createOrUpdateSecret(client kubernetes.Interface, resourceName, namespace string, data map[string][]byte, ownerReference metav1.OwnerReference) error {
	ctx := context.TODO()
	name := fmt.Sprintf("%s-monitor-envs", resourceName)
	secretClient := client.CoreV1().Secrets(namespace)

	secret, err := secretClient.Get(ctx, name, metav1.GetOptions{})
	if err != nil && errors.IsNotFound(err) {
		secret := corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:            name,
				OwnerReferences: []metav1.OwnerReference{ownerReference},
			},
			Data: data,
		}
		_, err := secretClient.Create(ctx, &secret, metav1.CreateOptions{})
		if err != nil {
			return err
		}
		log.Printf("...created secret '%s/%s'\n", namespace, name)
		// return nil
	} else if err != nil {
		return err
	} else {
		secret.Data = data
		_, err = secretClient.Update(ctx, secret, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
		log.Printf("...updated secret '%s/%s'\n", namespace, name)
		// return nil
	}
	return nil
}

func main() {
	configMapData := map[string]string{
		"CLUSTER_NAME": envOrPanic("CLUSTER_NAME"),
		"DBHOST":       envOrPanic("DBHOST"),
		"PGPORT":       envOrPanic("PGPORT", "5432"),
		"PGUSER":       envOrPanic("PGUSER"),
		"PGDATABASE":   envOrPanic("PGDATABASE"),
	}
	_ = configMapData
	secretData := map[string][]byte{
		"PGPASSWORD": []byte(envOrPanic("PGPASSWORD")),
	}
	_ = secretData

	config := kubernetesConfig(os.Getenv("KUBECONFIG"))
	client := kubernetes.NewForConfigOrDie(config)
	dynamicClient := dynamic.NewForConfigOrDie(config)
	group := tfv1alpha2.SchemeGroupVersion.Group
	version := tfv1alpha2.SchemeGroupVersion.Version
	terraformResource := schema.GroupVersionResource{Group: group, Version: version, Resource: "terraforms"}

	var handler cache.ResourceEventHandlerFuncs
	handler.AddFunc = func(obj interface{}) {
		tf := tfv1alpha2.Terraform{}
		b, err := json.Marshal(obj)
		if err != nil {
			log.Println("ERROR in add event", err)
		}
		err = json.Unmarshal(b, &tf)
		if err != nil {
			log.Println("ERROR in add event", err)
		}
		log.Println("add event:", tf.Name)
		ownerReference := metav1.OwnerReference{
			Name:       tf.Name,
			UID:        tf.UID,
			Kind:       "Terraform",
			APIVersion: "tf.isaaguilar.com/v1alpha2",
			Controller: boolp(true),
		}
		err = createOrUpdateConfigMap(client, tf.Name, tf.Namespace, configMapData, ownerReference)
		if err != nil {
			log.Println("ERROR in add event", err)
		}
		err = createOrUpdateSecret(client, tf.Name, tf.Namespace, secretData, ownerReference)
		if err != nil {
			log.Println("ERROR in add event", err)
		}
	}
	handler.UpdateFunc = func(old, new interface{}) {
		tfold := tfv1alpha2.Terraform{}
		tfnew := tfv1alpha2.Terraform{}

		bold, err := json.Marshal(old)
		if err != nil {
			log.Println("ERROR in update", err)
		}
		err = json.Unmarshal(bold, &tfold)
		if err != nil {
			log.Println("ERROR in update", err)
		}
		bnew, err := json.Marshal(new)
		if err != nil {
			log.Println("ERROR in update", err)
		}
		err = json.Unmarshal(bnew, &tfnew)
		if err != nil {
			log.Println("ERROR in update", err)
		}

		log.Println("update event: ", tfnew.Name)

		patches, err := jsonpatch.CreatePatch(bold, bnew)
		if err != nil {
			log.Println("ERROR in update", err)
		} else if len(patches) == 0 {
			log.Println("Nothing changed in latest update of ", tfnew.Name)
		} else {
			for _, patch := range patches {
				if patch.Operation == "replace" && patch.Path == "/metadata/generation" {
					log.Println("Moving from generation", tfold.Generation, "=>", tfnew.Generation)
					ownerReference := metav1.OwnerReference{
						Name:       tfnew.Name,
						UID:        tfnew.UID,
						Kind:       "Terraform",
						APIVersion: "tf.isaaguilar.com/v1alpha2",
						Controller: boolp(true),
					}
					err = createOrUpdateConfigMap(client, tfnew.Name, tfnew.Namespace, configMapData, ownerReference)
					if err != nil {
						log.Println("ERROR in update event", err)
					}
					err = createOrUpdateSecret(client, tfnew.Name, tfnew.Namespace, secretData, ownerReference)
					if err != nil {
						log.Println("ERROR in update event", err)
					}
				}
			}
		}

	}
	handler.DeleteFunc = func(obj interface{}) {
		log.Println("delete event")
	}

	informer := dynamicinformer.NewDynamicSharedInformerFactory(dynamicClient, 0)
	informer.ForResource(terraformResource).Informer().AddEventHandler(handler)

	log.Println("Start informer")
	stopCh := make(chan struct{})
	defer close(stopCh)
	informer.Start(stopCh)
	// TODO watch for stop
	<-stopCh
	// select {}
	log.Println("Stop informer")
	os.Exit(1) // should this be 0 instead?
}
