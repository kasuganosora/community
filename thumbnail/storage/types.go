package storage

// Config 存储配置.
type Config struct {
	Type       string `json:"type"` // local, s3, minio
	Bucket     string `json:"bucket"`
	Region     string `json:"region"`
	Endpoint   string `json:"endpoint"`
	PathPrefix string `json:"path_prefix"`
	LocalPath  string `json:"local_path"`
	PublicURL  string `json:"public_url"`
	AccessKey  string `json:"access_key"`
	SecretKey  string `json:"secret_key"`
}
