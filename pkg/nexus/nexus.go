package nexus

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"strings"

	"crypto/md5"
	"crypto/sha1"

	piperHttp "github.com/SAP/jenkins-library/pkg/http"
	"github.com/SAP/jenkins-library/pkg/log"
	"github.com/sirupsen/logrus"
)

type ArtifactDescription struct {
	ID         string `json:"artifactId"`
	Classifier string `json:"classifier"`
	Type       string `json:"type"`
	File       string `json:"file"`
}

type NexusUpload struct {
	baseURL   string
	Version   string
	Username  string
	Password  string
	artifacts []ArtifactDescription
	Logger    *logrus.Entry
}

func (nexusUpload *NexusUpload) initLogger() {
	if nexusUpload.Logger == nil {
		nexusUpload.Logger = log.Entry().WithField("package", "SAP/jenkins-library/pkg/nexusUpload")
	}
}

func (nexusUpload *NexusUpload) SetBaseURL(nexusUrl, nexusVersion, repository, groupID string) error {
	baseURL, err := getBaseURL(nexusUrl, nexusVersion, repository, groupID)
	if err != nil {
		return err
	}
	nexusUpload.baseURL = baseURL
	return nil
}

func (nexusUpload *NexusUpload) UploadArtifacts() {
	nexusUpload.initLogger()

	if nexusUpload.baseURL == "" {
		nexusUpload.Logger.Fatal("The NexusUpload object needs to be configured by calling SetBaseURL() first.")
	}

	fmt.Println(nexusUpload.artifacts)
	if len(nexusUpload.artifacts) == 0 {
		nexusUpload.Logger.Fatal("No artifacts to upload, call AddArtifact() or AddArtifactsFromJSON() first.")
	}

	client := nexusUpload.createHttpClient()

	for _, artifact := range nexusUpload.artifacts {
		url := getArtifactURL(nexusUpload.baseURL, nexusUpload.Version, artifact)

		uploadHash(client, artifact.File, url+".md5", md5.New(), 16)
		uploadHash(client, artifact.File, url+".sha1", sha1.New(), 20)
		uploadFile(client, artifact.File, url)
	}
}

func (nexusUpload *NexusUpload) AddArtifactsFromJSON(json string) error {
	artifacts, err := GetArtifacts(json)
	if err != nil {
		return err
	}
	if len(artifacts) == 0 {
		return errors.New("No artifact descriptions found in JSON string")
	}
	for _, artifact := range artifacts {
		err = validateArtifact(artifact)
		if err != nil {
			return err
		}
	}

	nexusUpload.artifacts = append(nexusUpload.artifacts, artifacts...)
	return nil
}

func validateArtifact(artifact ArtifactDescription) error {
	if artifact.File == "" || artifact.ID == "" || artifact.Type == "" {
		return errors.New(fmt.Sprintf("Artifact.File (%v), ID (%v) or Type (%v) is empty", artifact.File, artifact.ID, artifact.Type))
	}
	return nil
}

func (nexusUpload *NexusUpload) AddArtifact(artifact ArtifactDescription) error {
	err := validateArtifact(artifact)
	if err != nil {
		return err
	}
	nexusUpload.artifacts = append(nexusUpload.artifacts, artifact)
	return nil
}

func (nexusUpload *NexusUpload) GetArtifacts() []ArtifactDescription {
	return nexusUpload.artifacts
}

func GetArtifacts(artifactsAsJSON string) ([]ArtifactDescription, error) {
	var artifacts []ArtifactDescription
	err := json.Unmarshal([]byte(artifactsAsJSON), &artifacts)
	return artifacts, err
}

func (nexusUpload *NexusUpload) createHttpClient() *piperHttp.Client {
	client := piperHttp.Client{}
	clientOptions := piperHttp.ClientOptions{Username: nexusUpload.Username, Password: nexusUpload.Password, Logger: nexusUpload.Logger}
	client.SetOptions(clientOptions)
	return &client
}

func getBaseURL(nexusUrl, nexusVersion, repository, groupID string) (string, error) {
	baseUrl := nexusUrl
	switch nexusVersion {
	case "nexus2":
		baseUrl += "/content/repositories/"
	case "nexus3":
		baseUrl += "/repository/"
	default:
		return "", errors.New(fmt.Sprintf("Unsupported Nexus version '%s'", nexusVersion))
	}
	groupPath := strings.ReplaceAll(groupID, ".", "/")
	baseUrl += repository + "/" + groupPath + "/"
	return baseUrl, nil
}

func getArtifactURL(baseURL, version string, artifact ArtifactDescription) string {
	url := baseURL

	// Generate artifacte name including optional classifier
	artifactName := artifact.ID + "-" + version
	if len(artifact.Classifier) > 0 {
		artifactName += "-" + artifact.Classifier
	}
	artifactName += "." + artifact.Type

	url += artifact.ID + "/" + version + "/" + artifactName

	// Remove any double slashes, as Nexus does not like them, and prepend protocol
	url = "http://" + strings.ReplaceAll(url, "//", "/")

	return url
}

func uploadFile(client *piperHttp.Client, filePath, url string) {
	file, err := os.Open(filePath)
	if err != nil {
		log.Entry().WithError(err).Fatal("Failed to open artifact file ", filePath)
	}

	defer file.Close()

	err = uploadToNexus(client, file, url)
	if err != nil {
		log.Entry().WithError(err).Fatal("Failed to upload artifact ", filePath)
	}
}

func uploadHash(client *piperHttp.Client, filePath, url string, hash hash.Hash, length int) {
	hashReader, err := generateHashReader(filePath, hash, length)
	if err != nil {
		log.Entry().WithError(err).Fatal("Failed to generate hash")
	}
	err = uploadToNexus(client, hashReader, url)
	if err != nil {
		log.Entry().WithError(err).Fatal("Failed to upload hash")
	}
}

func uploadToNexus(client *piperHttp.Client, stream io.Reader, url string) error {
	response, err := client.SendRequest(http.MethodPut, url, stream, nil, nil)
	if err == nil {
		log.Entry().Info("Uploaded '"+url+"', response: ", response.StatusCode)
	}
	return err
}

func generateHashReader(filePath string, hash hash.Hash, length int) (io.Reader, error) {
	// Open file
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}

	defer file.Close()

	// Read file and feed the hash
	if _, err := io.Copy(hash, file); err != nil {
		return nil, err
	}

	// Get the requested number of bytes from the hash
	hashInBytes := hash.Sum(nil)[:length]

	// Convert the bytes to a string
	hexString := hex.EncodeToString(hashInBytes)

	// Finally create an io.Reader wrapping the string
	return strings.NewReader(hexString), nil
}