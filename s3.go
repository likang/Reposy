package main

import (
	"bytes"
	"compress/gzip"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
)

const HEADER_LOCAL_MODIFIED = "x-amz-meta-local-modified"
const HEADER_TOMBSTONE = "x-amz-meta-tombstone"

const INDEX_FILE = ".reposyindex"

type S3Config struct {
	Prefix          string `json:"prefix"`
	Endpoint        string `json:"endpoint"`
	Bucket          string `json:"bucket"`
	Region          string `json:"region"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
}

type S3Client struct {
	S3Config
}

type httpResponse struct {
	StatusCode int
	Headers    map[string]string
	Body       []byte
}

func NewS3Client(config *Config, repoConfig *RepositoryConfig) *S3Client {
	client := S3Client{}
	if err := json.Unmarshal(repoConfig.Raw, &client); err != nil {
		log.Fatalf("Failed to unmarshal S3 config: %v", err)
	}

	client.Prefix = strings.Trim(client.Prefix, "/") + "/"

	if client.Endpoint == "" {
		client.Endpoint = config.S3.Endpoint
	}
	if client.Bucket == "" {
		client.Bucket = config.S3.Bucket
	}
	if client.Region == "" {
		client.Region = config.S3.Region
	}
	if client.AccessKeyID == "" {
		client.AccessKeyID = config.S3.AccessKeyID
	}
	if client.SecretAccessKey == "" {
		client.SecretAccessKey = config.S3.SecretAccessKey
	}
	return &client
}

func (s3 *S3Client) List() (map[string]*RemoteItem, error) {
	// Download and parse index file from S3
	// indexKey := path.Join(s3.Prefix, INDEX_FILE)
	content, err := s3.Get(INDEX_FILE)
	if err != nil {
		if exist, err := s3.Exist(INDEX_FILE); !exist && err == nil {
			return make(map[string]*RemoteItem), nil
		}
		return nil, fmt.Errorf("failed to download index file: %v", err)
	}

	// Create a gzip reader
	gzReader, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %v", err)
	}
	defer gzReader.Close()

	// Read and decode JSON content
	var fileItems map[string]*RemoteItem
	decoder := json.NewDecoder(gzReader)
	if err := decoder.Decode(&fileItems); err != nil {
		return nil, fmt.Errorf("failed to decode index file content: %v", err)
	}

	return fileItems, nil
}

func (s3 *S3Client) Put(data []byte, modTime time.Time, slashPath string) error {
	if slashPath == INDEX_FILE {
		return nil
	}

	var headers = map[string]string{
		HEADER_LOCAL_MODIFIED: fmt.Sprintf("%d", modTime.Unix()),
		HEADER_TOMBSTONE:      "0",
	}

	fullPath := path.Join(s3.Prefix, slashPath)
	resp, err := s3.request("PUT", fullPath, data, headers, nil)

	if err == nil && resp.StatusCode != 200 {
		return fmt.Errorf("failed to put %s: %s", slashPath, resp.Body)
	}
	return err
}

// download file from s3
func (s3 *S3Client) Get(slashPath string) (content []byte, err error) {
	fullPath := path.Join(s3.Prefix, slashPath)
	resp, err := s3.request("GET", fullPath, nil, nil, nil)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to download file %s: %s", slashPath, resp.Body)
	}

	return resp.Body, nil
}

func (s3 *S3Client) Exist(slashPath string) (bool, error) {
	fullPath := path.Join(s3.Prefix, slashPath)
	resp, err := s3.request("HEAD", fullPath, nil, nil, nil)
	if err != nil {
		return false, err
	}
	if resp.StatusCode == 404 {
		return false, nil
	}
	if resp.StatusCode == 200 {
		return true, nil
	}
	return false, fmt.Errorf("failed to check %s: %s", slashPath, resp.Body)
}

// mark file in s3 as tombstone
func (s3 *S3Client) MarkTombstone(slashPath string) error {
	var headers = map[string]string{
		HEADER_LOCAL_MODIFIED: fmt.Sprintf("%d", time.Now().Unix()),
		HEADER_TOMBSTONE:      "1",
	}

	fullPath := path.Join(s3.Prefix, slashPath)
	resp, err := s3.request("PUT", fullPath, nil, headers, nil)

	if err == nil && resp.StatusCode != 200 {
		return fmt.Errorf("failed to mark %s as tombstone: %s", slashPath, resp.Body)
	}

	return err
}

func (s3 *S3Client) Delete(slashPath string) error {
	fullPath := path.Join(s3.Prefix, slashPath)
	resp, err := s3.request("DELETE", fullPath, nil, nil, nil)
	if err == nil && resp.StatusCode != 204 {
		return fmt.Errorf("failed to delete %s: %s", slashPath, resp.Body)
	}
	return err
}

func (s3 *S3Client) Finish(meta map[string]*RemoteItem, changed bool) error {
	if !changed {
		return nil
	}

	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("failed to marshal meta: %v", err)
	}

	var buf bytes.Buffer
	gzWriter := gzip.NewWriter(&buf)
	if _, err = gzWriter.Write(metaBytes); err != nil {
		gzWriter.Close()
		return fmt.Errorf("failed to write meta to gzip writer: %v", err)
	}
	if err = gzWriter.Close(); err != nil {
		return fmt.Errorf("failed to close gzip writer: %v", err)
	}

	// put to s3 directly without using .Put()
	fullPath := path.Join(s3.Prefix, INDEX_FILE)
	resp, err := s3.request("PUT", fullPath, buf.Bytes(), nil, nil)

	if err == nil && resp.StatusCode != 200 {
		return fmt.Errorf("failed to put %s: %s", INDEX_FILE, resp.Body)
	}

	return err
}

func (s3 *S3Client) request(method string, slashPath string, payload []byte, headers map[string]string, uriParams map[string]string) (*httpResponse, error) {
	pathWithParams := slashPath
	if len(uriParams) > 0 {
		query := url.Values{}
		for k, v := range uriParams {
			query.Add(k, v)
		}
		pathWithParams += "?" + query.Encode()
	}

	return _s3Request(
		method,
		pathWithParams,
		payload,
		s3.AccessKeyID,
		s3.SecretAccessKey,
		s3.Region,
		fmt.Sprintf("%s.%s", s3.Bucket, s3.Endpoint),
		headers)

}

func _s3Request(method string, uri string, payload []byte, awsAccessKey string, awsSecretKey string, region string, host string, headers map[string]string) (*httpResponse, error) {
	const service = "s3"

	if !strings.HasPrefix(uri, "/") {
		uri = "/" + uri
	}

	if payload == nil {
		payload = []byte{}
	}

	parsedURI, _ := url.Parse(uri)

	canonicalURI := awsEscapePath(parsedURI.Path, false)

	query := parsedURI.Query()
	queryPairs := make([][2]string, 0, len(query))
	for k, v := range query {
		if len(v) > 0 {
			queryPairs = append(queryPairs, [2]string{url.PathEscape(k), url.PathEscape(v[0])})
		} else {
			queryPairs = append(queryPairs, [2]string{url.PathEscape(k), ""})
		}
	}
	sort.Slice(queryPairs, func(i, j int) bool {
		return queryPairs[i][0] < queryPairs[j][0]
	})
	canonicalQueryString := strings.Join(func() []string {
		queryLines := make([]string, len(queryPairs))
		for i, pair := range queryPairs {
			queryLines[i] = strings.Join(pair[:], "=")
		}
		return queryLines

	}(), "&")

	payloadHash := sha256.Sum256(payload)
	payloadHashHex := hex.EncodeToString(payloadHash[:])

	t := time.Now().UTC()
	amzDate := t.Format("20060102T150405Z")
	dateStamp := t.Format("20060102")

	if headers == nil {
		headers = make(map[string]string)
	}
	headers["host"] = host
	headers["x-amz-content-sha256"] = payloadHashHex
	headers["x-amz-date"] = amzDate

	headersWithLowerKey := make([][2]string, 0, len(headers))
	for k, v := range headers {
		headersWithLowerKey = append(headersWithLowerKey, [2]string{strings.ToLower(k), strings.TrimSpace(v)})
	}
	sort.Slice(headersWithLowerKey, func(i, j int) bool {
		return headersWithLowerKey[i][0] < headersWithLowerKey[j][0]
	})

	canonicalHeaders := strings.Join(func() []string {
		headersLines := make([]string, len(headersWithLowerKey))
		for i, h := range headersWithLowerKey {
			headersLines[i] = fmt.Sprintf("%s:%s\n", h[0], h[1])
		}
		return headersLines
	}(), "")
	signedHeaders := strings.Join(func() []string {
		keys := make([]string, len(headersWithLowerKey))
		for i, h := range headersWithLowerKey {
			keys[i] = h[0]
		}
		return keys
	}(), ";")

	canonicalRequest := strings.Join([]string{
		method,
		canonicalURI,
		canonicalQueryString,
		canonicalHeaders,
		signedHeaders,
		payloadHashHex,
	}, "\n")
	canonicalRequestBytes := []byte(canonicalRequest)
	canonicalRequestHash := sha256.Sum256(canonicalRequestBytes[:])

	algorithm := "AWS4-HMAC-SHA256"
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, region, service)
	signingKey := getSignatureKey(awsSecretKey, dateStamp, region, service)
	signature := hex.EncodeToString(sign(signingKey, strings.Join([]string{
		algorithm,
		amzDate,
		credentialScope,
		hex.EncodeToString(canonicalRequestHash[:]),
	}, "\n")))

	authorizationHeader := fmt.Sprintf("%s Credential=%s/%s,SignedHeaders=%s,Signature=%s",
		algorithm, awsAccessKey, credentialScope, signedHeaders, signature)

	headers["Authorization"] = authorizationHeader

	client := http.Client{}
	url := "https://" + host + canonicalURI
	if canonicalQueryString != "" {
		url += "?" + canonicalQueryString
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respStatus := resp.StatusCode
	respHeaders := make(map[string]string)
	for k, v := range resp.Header {
		respHeaders[k] = v[0]
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return &httpResponse{respStatus, respHeaders, respBody}, nil
}

func sign(key []byte, msg string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(msg))
	return h.Sum(nil)
}

func getSignatureKey(secretKey, dateStamp, regionName, serviceName string) []byte {
	kDate := sign([]byte("AWS4"+secretKey), dateStamp)
	kRegion := sign(kDate, regionName)
	kService := sign(kRegion, serviceName)
	kSigning := sign(kService, "aws4_request")
	return kSigning
}

// https://github.com/aws/smithy-go/blob/main/encoding/httpbinding/path_replace.go
// EscapePath escapes part of a URL path in Amazon style.
func awsEscapePath(path string, encodeSep bool) string {
	var buf bytes.Buffer
	for i := 0; i < len(path); i++ {
		c := path[i]
		if noEscape[c] || (c == '/' && !encodeSep) {
			buf.WriteByte(c)
		} else {
			fmt.Fprintf(&buf, "%%%02X", c)
		}
	}
	return buf.String()
}

var noEscape [256]bool = func() [256]bool {
	var ne [256]bool
	for i := 0; i < len(ne); i++ {
		// AWS expects every character except these to be escaped
		ne[i] = (i >= 'A' && i <= 'Z') ||
			(i >= 'a' && i <= 'z') ||
			(i >= '0' && i <= '9') ||
			i == '-' ||
			i == '.' ||
			i == '_' ||
			i == '~'
	}
	return ne
}()
