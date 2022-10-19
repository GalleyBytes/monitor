package tfohttpclient

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
)

func ResourceSpec() ([]byte, error) {
	certFile := "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	tokenFile := "/var/run/secrets/kubernetes.io/serviceaccount/token"
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	if host == "" {
		host = "kubernetes.default.svc"
	}
	group := "tf.isaaguilar.com/v1alpha2"
	namespace := os.Getenv("TFO_NAMESPACE")
	resource := os.Getenv("TFO_RESOURCE")
	url := fmt.Sprintf("https://%s/apis/%s/namespaces/%s/terraforms/%s", host, group, namespace, resource)

	// Get the SystemCertPool, continue with an empty pool on error
	rootCAs, _ := x509.SystemCertPool()
	if rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}

	// Read in the cert file
	certs, err := ioutil.ReadFile(certFile)
	if err != nil {
		return []byte{}, fmt.Errorf("failed to append %q to RootCAs: %v", certFile, err)
	}

	// Append our cert to the system pool
	if ok := rootCAs.AppendCertsFromPEM(certs); !ok {
		return []byte{}, fmt.Errorf("no certs appended, using system certs only")
	}

	// Trust the augmented cert pool in our client
	config := &tls.Config{
		InsecureSkipVerify: false,
		RootCAs:            rootCAs,
	}
	tr := &http.Transport{TLSClientConfig: config}

	req, err := http.NewRequest(http.MethodGet, url, bytes.NewBuffer([]byte{}))
	if err != nil {
		return []byte{}, err
	}

	authToken, err := ioutil.ReadFile(tokenFile)
	if err != nil {
		return []byte{}, fmt.Errorf("failed to load token from tokenFile '%s': %s", tokenFile, err.Error())
	}
	req.Header = map[string][]string{
		"Authorization": {fmt.Sprintf("Bearer %s", string(authToken))},
	}
	client := &http.Client{
		Transport: tr,
	}
	resp, err := client.Do(req)
	if err != nil {
		return []byte{}, fmt.Errorf("errored when sending request to the server: %s", err.Error())
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return []byte{}, err
	}

	// Extract the spec
	var respData interface{}
	err = json.Unmarshal(body, &respData)
	if err != nil {
		return []byte{}, fmt.Errorf("response body failed to unmarshal: %s", err.Error())
	}

	specJson, err := json.Marshal(respData.(map[string]interface{})["spec"])
	if err != nil {
		return []byte{}, fmt.Errorf("could not find spec in response data: %s", err.Error())
	}

	return specJson, nil
}
