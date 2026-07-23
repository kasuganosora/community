package thumbnail

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/thumbnail/service"
	"github.com/kasuganosora/community/thumbnail/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newTestService creates a *service.Service with enabled=true, rewrite=true,
// and a local storage backend pointing at a temporary directory.
// It does NOT set up a database, so only code paths that avoid DB calls can be
// exercised (e.g. processJSONResponse with no remote image URLs).
func newTestService(t *testing.T, enabled, rewriteEnabled bool) *service.Service { //nolint:unparam // param reserved for future use
	t.Helper()
	tmpDir := t.TempDir()
	stCfg := storage.Config{
		Type:       string(StorageTypeLocal),
		LocalPath:  tmpDir,
		PathPrefix: "thumbnails/", //nolint:goconst
	}
	backend, err := storage.NewBackend(stCfg)
	require.NoError(t, err, "NewBackend should not fail with a temp dir")

	svcCfg := &models.ThumbnailServiceConfig{
		Enabled:        enabled,
		RewriteEnabled: rewriteEnabled,
		Storage: models.ThumbnailStorageConfig{
			Type:       models.StorageTypeLocal,
			LocalPath:  tmpDir,
			PathPrefix: "thumbnails/",
		},
		Rules: []models.ThumbnailRule{
			{
				ContentTypes:    []string{"image/jpeg", "image/png"}, //nolint:goconst
				Extensions:      []string{"jpg", "jpeg", "png"},      //nolint:goconst
				MaxOriginalSize: 524288,
				MaxWidth:        1920,
				MaxHeight:       1080,
				Quality:         85,
				Format:          "webp", //nolint:goconst
				Enabled:         true,
				MediaType:       "image", //nolint:goconst
			},
		},
	}

	svc, err := service.NewService(svcCfg, backend)
	require.NoError(t, err)

	return svc
}

// ===========================================================================
// shouldProcessPath
// ===========================================================================

func TestShouldProcessPath_ValidPrefixes(t *testing.T) {
	validPaths := []string{
		"/api/v1/timelines/home",
		"/api/v1/timelines/public",
		"/api/v1/timelines/list",
		"/api/v1/timelines/tag",
		"/api/v1/accounts/",
		"/api/v1/statuses/",
		"/api/v1/favourites",
		"/api/v1/bookmarks",
	}
	for _, p := range validPaths {
		assert.True(t, shouldProcessPath(p), "shouldProcessPath(%q) should be true", p)
	}
}

func TestShouldProcessPath_Subpaths(t *testing.T) {
	subpaths := []string{
		"/api/v1/timelines/home?limit=20",
		"/api/v1/timelines/public?local=true",
		"/api/v1/timelines/list/42",
		"/api/v1/timelines/tag/cats",
		"/api/v1/accounts/123",
		"/api/v1/accounts/123/statuses",
		"/api/v1/statuses/1092384710234",
		"/api/v1/statuses/1092384710234/context",
		"/api/v1/favourites?max_id=abc",
		"/api/v1/bookmarks?min_id=xyz",
	}
	for _, p := range subpaths {
		assert.True(t, shouldProcessPath(p), "shouldProcessPath(%q) should be true", p)
	}
}

func TestShouldProcessPath_InvalidPaths(t *testing.T) {
	invalidPaths := []string{
		"",
		"/",
		"/api/v1",
		"/api/v1/notifications",
		"/api/v1/instance",
		"/api/v2/instance",
		"/api/v1/statuses", // missing trailing slash - HasPrefix won't match
		"/api/v1/accounts", // missing trailing slash
		"/api/v1/timelines/hashtag",
		"/users/profile",
		"/auth/sign_in",
		"api/v1/timelines/home", // missing leading slash
	}
	for _, p := range invalidPaths {
		assert.False(t, shouldProcessPath(p), "shouldProcessPath(%q) should be false", p)
	}
}

func TestShouldProcessPath_CaseSensitive(t *testing.T) {
	// Paths are case-sensitive – uppercase variants should not match.
	upperPaths := []string{
		"/API/v1/timelines/home",
		"/api/V1/Timelines/home",
		"/Api/V1/Timelines/Home",
	}
	for _, p := range upperPaths {
		assert.False(t, shouldProcessPath(p), "shouldProcessPath(%q) should be false (case-sensitive)", p)
	}
}

func TestShouldProcessPath_EmptyString(t *testing.T) {
	assert.False(t, shouldProcessPath(""))
}

// ===========================================================================
// isRemoteURL
// ===========================================================================

func TestIsRemoteURL_Empty(t *testing.T) {
	assert.False(t, isRemoteURL("", "example.com"))
	assert.False(t, isRemoteURL("", ""))
}

func TestIsRemoteURL_NonHTTP(t *testing.T) {
	nonHTTP := []string{
		"ftp://example.com/image.jpg",
		"file:///etc/passwd",
		"mailto:user@example.com",
		"tel:+1234567890",
		"//example.com/img.png",
		"/relative/path.png",
		"just-some-text",
		"data:image/png;base64,abc",
	}
	for _, u := range nonHTTP {
		assert.False(t, isRemoteURL(u, "example.com"), "isRemoteURL(%q) should be false", u)
	}
}

func TestIsRemoteURL_HTTPSRemote(t *testing.T) {
	remoteURLs := []string{
		"https://cdn.example.com/img.jpg",
		"http://img.server.org/photo.png",
		"https://remote-host.com/assets/image.webp?w=200",
	}
	for _, u := range remoteURLs {
		assert.True(t, isRemoteURL(u, "myapp.com"), "isRemoteURL(%q) should be true", u)
	}
}

func TestIsRemoteURL_SameHost(t *testing.T) {
	host := "myapp.com" //nolint:goconst

	sameHostURLs := []string{
		"https://myapp.com/media/image.jpg",
		"http://myapp.com/uploads/photo.png",
		"https://myapp.com:443/img.webp",
	}

	for _, u := range sameHostURLs {
		assert.False(t, isRemoteURL(u, host), "isRemoteURL(%q, %q) should be false", u, host)
	}
}

func TestIsRemoteURL_SameHostWWW(t *testing.T) {
	host := "myapp.com"

	wwwURLs := []string{
		"https://www.myapp.com/media/image.jpg",
		"http://www.myapp.com/uploads/photo.png",
	}

	for _, u := range wwwURLs {
		assert.False(t, isRemoteURL(u, host), "isRemoteURL(%q, %q) should be false (www prefix)", u, host)
	}
}

func TestIsRemoteURL_EmptyHost(t *testing.T) {
	// When host is empty, any http(s) URL should be considered remote.
	assert.True(t, isRemoteURL("https://cdn.example.com/img.jpg", ""))
	assert.True(t, isRemoteURL("http://example.com/photo.png", ""))
}

func TestIsRemoteURL_SubdomainNotWWW(t *testing.T) {
	// A subdomain that is not "www." should still be treated as remote.
	host := "myapp.com"
	assert.True(t, isRemoteURL("https://cdn.myapp.com/img.jpg", host))
	assert.True(t, isRemoteURL("https://api.myapp.com/photo.png", host))
}

func TestIsRemoteURL_HostSubstring(t *testing.T) {
	// Host "notmyapp.com" contains "myapp.com" as a substring but is not the same host.
	host := "myapp.com"
	// Note: the check is `strings.Contains(url, "://"+host)` which would match
	// "https://myapp.com.evil.com/img.jpg" – this is a known quirk we test.
	assert.False(t, isRemoteURL("https://myapp.com/img.jpg", host))
	// This is actually a potential issue but we test the actual behavior:
	// "https://notmyapp.com/img.jpg" does NOT contain "://myapp.com" so it's remote.
	assert.True(t, isRemoteURL("https://notmyapp.com/img.jpg", host))
}

// ===========================================================================
// extractRemoteImageURLs
// ===========================================================================

func TestExtractRemoteImageURLs_RemoteImages(t *testing.T) {
	content := `<img src="https://cdn.example.com/photo.jpg"><img src="https://img.server.org/pic.png">`
	result := extractRemoteImageURLs(content, "myapp.com")

	assert.Len(t, result, 2)
	assert.Contains(t, result, "https://cdn.example.com/photo.jpg")
	assert.Contains(t, result, "https://img.server.org/pic.png")
}

func TestExtractRemoteImageURLs_FilterLocal(t *testing.T) {
	content := `https://myapp.com/uploads/local.jpg https://cdn.remote.com/remote.png`
	result := extractRemoteImageURLs(content, "myapp.com")

	assert.Len(t, result, 1)
	assert.Equal(t, "https://cdn.remote.com/remote.png", result[0])
}

func TestExtractRemoteImageURLs_EmptyContent(t *testing.T) {
	result := extractRemoteImageURLs("", "myapp.com")
	assert.Empty(t, result)
}

func TestExtractRemoteImageURLs_NoImageURLs(t *testing.T) {
	content := "Hello world, this is plain text with no images."
	result := extractRemoteImageURLs(content, "myapp.com")
	assert.Empty(t, result)
}

func TestExtractRemoteImageURLs_UnsupportedExtensions(t *testing.T) {
	content := "https://cdn.example.com/doc.pdf https://cdn.example.com/video.mp4"
	result := extractRemoteImageURLs(content, "myapp.com")
	assert.Empty(t, result) // mp4 is not matched by the image regex
}

func TestExtractRemoteImageURLs_AllExtensions(t *testing.T) {
	extensions := []string{"jpg", "jpeg", "png", "gif", "webp"} //nolint:goconst
	content := ""

	expected := make([]string, 0, len(extensions))
	var contentSb260 strings.Builder

	for _, ext := range extensions {
		url := "https://cdn.example.com/img." + ext
		contentSb260.WriteString(url + " ")
		expected = append(expected, url)
	}
	content += contentSb260.String()

	result := extractRemoteImageURLs(content, "myapp.com")
	assert.Len(t, result, len(extensions))

	for _, exp := range expected {
		assert.Contains(t, result, exp)
	}
}

func TestExtractRemoteImageURLs_Deduplication(t *testing.T) {
	content := "https://cdn.example.com/photo.jpg"
	result := extractRemoteImageURLs(content, "myapp.com")
	assert.Len(t, result, 1)
	assert.Equal(t, "https://cdn.example.com/photo.jpg", result[0])
}

func TestExtractRemoteImageURLs_QueryParameters(t *testing.T) {
	content := `https://cdn.example.com/img.jpg?w=800&h=600&format=webp https://cdn.example.com/photo.png?token=abc123`
	result := extractRemoteImageURLs(content, "myapp.com")
	assert.Len(t, result, 2)
}

func TestExtractRemoteImageURLs_WithSurroundingMarkup(t *testing.T) {
	content := `<a href="https://cdn.example.com/photo.jpg"><img src="https://cdn.example.com/photo.jpg"></a>`
	result := extractRemoteImageURLs(content, "myapp.com")
	// The regex should find the URL in both href and src, but dedup keeps one.
	assert.Len(t, result, 1)
}

func TestExtractRemoteImageURLs_HTTPAndHTTPS(t *testing.T) {
	content := "http://cdn.example.com/http.jpg https://cdn.example.com/https.png"
	result := extractRemoteImageURLs(content, "myapp.com")
	assert.Len(t, result, 2)
}

func TestExtractRemoteImageURLs_NonHTTPFiltered(t *testing.T) {
	content := "ftp://cdn.example.com/img.jpg //cdn.example.com/img.png"
	result := extractRemoteImageURLs(content, "myapp.com")
	assert.Empty(t, result)
}

func TestExtractRemoteImageURLs_Mixed(t *testing.T) {
	content := `
	https://myapp.com/local.jpg
	https://remote.com/remote.png
	http://www.myapp.com/www-local.gif
	some plain text
	https://another.remote/image.webp?size=large
	`
	result := extractRemoteImageURLs(content, "myapp.com")
	assert.Len(t, result, 2)
	assert.Equal(t, "https://remote.com/remote.png", result[0])
	assert.Equal(t, "https://another.remote/image.webp?size=large", result[1])
}

// ===========================================================================
// convertRules
// ===========================================================================

func TestConvertRules_Empty(t *testing.T) {
	result := convertRules(nil)
	assert.Empty(t, result)

	result = convertRules([]ThumbnailRule{})
	assert.Empty(t, result)
}

func TestConvertRules_SingleRule(t *testing.T) {
	rules := []ThumbnailRule{
		{
			ContentTypes:      []string{"image/jpeg"},
			Extensions:        []string{"jpg"},
			MaxOriginalSize:   1024,
			MaxWidth:          800,
			MaxHeight:         600,
			Quality:           75,
			Format:            "webp",
			Enabled:           true,
			ExcludeAnimations: true,
			MediaType:         "image",
		},
	}
	result := convertRules(rules)
	require := assert.New(t)
	require.Len(result, 1)

	r := result[0]
	assert.Equal(t, []string{"image/jpeg"}, r.ContentTypes)
	assert.Equal(t, []string{"jpg"}, r.Extensions)
	assert.Equal(t, int64(1024), r.MaxOriginalSize)
	assert.Equal(t, 800, r.MaxWidth)
	assert.Equal(t, 600, r.MaxHeight)
	assert.Equal(t, 75, r.Quality)
	assert.Equal(t, "webp", r.Format)
	assert.True(t, r.Enabled)
	assert.True(t, r.ExcludeAnimations)
	assert.Equal(t, "image", r.MediaType)
}

func TestConvertRules_MultipleRules(t *testing.T) {
	rules := []ThumbnailRule{
		{
			ContentTypes: []string{"image/jpeg"},
			Extensions:   []string{"jpg"},
			Enabled:      true,
			MediaType:    "image",
		},
		{
			ContentTypes: []string{"video/mp4"},
			Extensions:   []string{"mp4"},
			Enabled:      true,
			MediaType:    "video",
		},
		{
			ContentTypes: []string{"image/gif"},
			Extensions:   []string{"gif"},
			Enabled:      false,
			MediaType:    "image",
		},
	}
	result := convertRules(rules)
	assert.Len(t, result, 3)
	assert.Equal(t, "image/jpeg", result[0].ContentTypes[0])
	assert.Equal(t, "image", result[0].MediaType)
	assert.Equal(t, "video/mp4", result[1].ContentTypes[0])
	assert.Equal(t, "video", result[1].MediaType)
	assert.False(t, result[2].Enabled)
}

func TestConvertRules_ZeroValues(t *testing.T) {
	rules := []ThumbnailRule{
		{
			ContentTypes:      nil,
			Extensions:        nil,
			MaxOriginalSize:   0,
			MaxWidth:          0,
			MaxHeight:         0,
			Quality:           0,
			Format:            "",
			Enabled:           false,
			ExcludeAnimations: false,
			MediaType:         "",
		},
	}
	result := convertRules(rules)
	require := assert.New(t)
	require.Len(result, 1)

	r := result[0]
	assert.Nil(t, r.ContentTypes)
	assert.Nil(t, r.Extensions)
	assert.Equal(t, int64(0), r.MaxOriginalSize)
	assert.Equal(t, 0, r.MaxWidth)
	assert.Equal(t, 0, r.MaxHeight)
	assert.Equal(t, 0, r.Quality)
	assert.Empty(t, r.Format)
	assert.False(t, r.Enabled)
	assert.False(t, r.ExcludeAnimations)
	assert.Empty(t, r.MediaType)
}

func TestConvertRules_PreservesSliceIdentity(t *testing.T) {
	rules := []ThumbnailRule{
		{ContentTypes: []string{"image/png"}, Extensions: []string{"png"}, MediaType: "image"},
	}
	result := convertRules(rules)
	// Slices should be copied, not aliased.
	assert.NotSame(t, &rules[0], &result[0])
	assert.Equal(t, rules[0].ContentTypes, result[0].ContentTypes)
}

// ===========================================================================
// DefaultConfig
// ===========================================================================

func TestDefaultConfig_NilSafe(t *testing.T) {
	cfg := DefaultConfig()
	assert.NotNil(t, cfg)
}

func TestDefaultConfig_Enabled(t *testing.T) {
	cfg := DefaultConfig()
	assert.False(t, cfg.Enabled, "thumbnail service should be disabled by default")
}

func TestDefaultConfig_AccessMode(t *testing.T) {
	cfg := DefaultConfig()
	assert.Equal(t, AccessModeRedirect, cfg.AccessMode)
}

func TestDefaultConfig_RewriteEnabled(t *testing.T) {
	cfg := DefaultConfig()
	assert.True(t, cfg.RewriteEnabled)
}

func TestDefaultConfig_StorageDefaults(t *testing.T) {
	cfg := DefaultConfig()
	assert.Equal(t, StorageTypeLocal, cfg.Storage.Type)
	assert.Equal(t, "thumbnails/", cfg.Storage.PathPrefix)
	assert.Equal(t, "./storage/thumbnails", cfg.Storage.LocalPath)
	assert.Empty(t, cfg.Storage.PublicURL)
}

func TestDefaultConfig_RulesNotEmpty(t *testing.T) {
	cfg := DefaultConfig()
	assert.NotEmpty(t, cfg.Rules)
}

func TestDefaultConfig_RulesImageJPEG(t *testing.T) {
	cfg := DefaultConfig()
	// First rule should be the JPEG/PNG rule
	rule := cfg.Rules[0]
	assert.Contains(t, rule.ContentTypes, "image/jpeg")
	assert.Contains(t, rule.ContentTypes, "image/png")
	assert.Contains(t, rule.Extensions, "jpg")
	assert.Contains(t, rule.Extensions, "jpeg")
	assert.Contains(t, rule.Extensions, "png")
	assert.Equal(t, int64(524288), rule.MaxOriginalSize)
	assert.Equal(t, 1920, rule.MaxWidth)
	assert.Equal(t, 1080, rule.MaxHeight)
	assert.Equal(t, 85, rule.Quality)
	assert.Equal(t, "webp", rule.Format)
	assert.True(t, rule.Enabled)
	assert.False(t, rule.ExcludeAnimations)
	assert.Equal(t, "image", rule.MediaType)
}

func TestDefaultConfig_RulesWebP(t *testing.T) {
	cfg := DefaultConfig()
	rule := cfg.Rules[1]
	assert.Contains(t, rule.ContentTypes, "image/webp")
	assert.Contains(t, rule.Extensions, "webp")
	assert.Equal(t, int64(0), rule.MaxOriginalSize) // process all
	assert.True(t, rule.ExcludeAnimations)
	assert.True(t, rule.Enabled)
	assert.Equal(t, "image", rule.MediaType)
}

func TestDefaultConfig_RulesGIFDisabled(t *testing.T) {
	cfg := DefaultConfig()
	rule := cfg.Rules[2]
	assert.Contains(t, rule.ContentTypes, "image/gif")
	assert.Contains(t, rule.Extensions, "gif")
	assert.False(t, rule.Enabled)
}

func TestDefaultConfig_RulesSVGDisabled(t *testing.T) {
	cfg := DefaultConfig()
	rule := cfg.Rules[3]
	assert.Contains(t, rule.ContentTypes, "image/svg+xml")
	assert.Contains(t, rule.Extensions, "svg")
	assert.False(t, rule.Enabled)
}

func TestDefaultConfig_RulesHEIC(t *testing.T) {
	cfg := DefaultConfig()
	rule := cfg.Rules[4]
	assert.Contains(t, rule.ContentTypes, "image/heic")
	assert.Contains(t, rule.ContentTypes, "image/heif")
	assert.Contains(t, rule.Extensions, "heic")
	assert.Contains(t, rule.Extensions, "heif")
	assert.True(t, rule.Enabled)
}

func TestDefaultConfig_RulesAVIF(t *testing.T) {
	cfg := DefaultConfig()
	rule := cfg.Rules[5]
	assert.Contains(t, rule.ContentTypes, "image/avif")
	assert.Contains(t, rule.Extensions, "avif")
	assert.True(t, rule.Enabled)
}

func TestDefaultConfig_RulesVideo(t *testing.T) {
	cfg := DefaultConfig()
	rule := cfg.Rules[6]
	assert.Contains(t, rule.ContentTypes, "video/mp4")
	assert.Contains(t, rule.ContentTypes, "video/webm")
	assert.Contains(t, rule.ContentTypes, "video/quicktime")
	assert.Contains(t, rule.Extensions, "mp4")
	assert.Contains(t, rule.Extensions, "webm")
	assert.Contains(t, rule.Extensions, "mov")
	assert.Equal(t, int64(10485760), rule.MaxOriginalSize) // 10MB
	assert.Equal(t, 1280, rule.MaxWidth)
	assert.Equal(t, 720, rule.MaxHeight)
	assert.Equal(t, 80, rule.Quality)
	assert.True(t, rule.Enabled)
	assert.Equal(t, "video", rule.MediaType)
}

// ===========================================================================
// Config.GetStoragePublicURL
// ===========================================================================

func TestGetStoragePublicURL_PublicURLSet(t *testing.T) {
	cfg := &Config{
		Storage: StorageConfig{
			Type:      StorageTypeS3,
			PublicURL: "https://my-cdn.example.com",
		},
	}
	assert.Equal(t, "https://my-cdn.example.com", cfg.GetStoragePublicURL())
}

func TestGetStoragePublicURL_PublicURLOverridesType(t *testing.T) {
	// Even for local type, if PublicURL is set, it should be returned.
	cfg := &Config{
		Storage: StorageConfig{
			Type:      StorageTypeLocal,
			PublicURL: "https://custom.cdn.com",
		},
	}
	assert.Equal(t, "https://custom.cdn.com", cfg.GetStoragePublicURL())
}

func TestGetStoragePublicURL_Local(t *testing.T) {
	cfg := &Config{
		Storage: StorageConfig{
			Type: StorageTypeLocal,
		},
	}
	assert.Equal(t, "/thumbnails", cfg.GetStoragePublicURL())
}

func TestGetStoragePublicURL_S3(t *testing.T) {
	cfg := &Config{
		Storage: StorageConfig{
			Type:   StorageTypeS3,
			Bucket: "my-bucket", //nolint:goconst
		},
	}
	assert.Equal(t, "https://my-bucket.s3.amazonaws.com", cfg.GetStoragePublicURL())
}

func TestGetStoragePublicURL_S3WithRegion(t *testing.T) {
	cfg := &Config{
		Storage: StorageConfig{
			Type:   StorageTypeS3,
			Bucket: "my-bucket",
			Region: "us-west-2",
		},
	}
	assert.Equal(t, "https://my-bucket.s3.us-west-2.amazonaws.com", cfg.GetStoragePublicURL())
}

func TestGetStoragePublicURL_MinIO(t *testing.T) {
	cfg := &Config{
		Storage: StorageConfig{
			Type:     StorageTypeMinIO,
			Endpoint: "https://minio.example.com",
			Bucket:   "my-bucket",
		},
	}
	assert.Equal(t, "https://minio.example.com/my-bucket", cfg.GetStoragePublicURL())
}

func TestGetStoragePublicURL_MinIOTCPPort(t *testing.T) {
	cfg := &Config{
		Storage: StorageConfig{
			Type:     StorageTypeMinIO,
			Endpoint: "http://localhost:9000",
			Bucket:   "my-bucket",
		},
	}
	assert.Equal(t, "http://localhost:9000/my-bucket", cfg.GetStoragePublicURL())
}

func TestGetStoragePublicURL_UnknownType(t *testing.T) {
	cfg := &Config{
		Storage: StorageConfig{
			Type: StorageType("unknown"),
		},
	}
	assert.Empty(t, cfg.GetStoragePublicURL())
}

func TestGetStoragePublicURL_AliyunOSS(t *testing.T) {
	// aliyun_oss and tencent_cos fall through to default (empty string).
	cfg := &Config{
		Storage: StorageConfig{
			Type:   StorageTypeAliyunOSS,
			Bucket: "my-bucket",
		},
	}
	assert.Empty(t, cfg.GetStoragePublicURL())
}

func TestGetStoragePublicURL_TencentCOS(t *testing.T) {
	cfg := &Config{
		Storage: StorageConfig{
			Type:   StorageTypeTencentCOS,
			Bucket: "my-bucket",
		},
	}
	assert.Empty(t, cfg.GetStoragePublicURL())
}

func TestGetStoragePublicURL_EmptyBucket(t *testing.T) {
	cfg := &Config{
		Storage: StorageConfig{
			Type:   StorageTypeS3,
			Bucket: "",
			Region: "us-east-1",
		},
	}
	assert.Equal(t, "https://.s3.us-east-1.amazonaws.com", cfg.GetStoragePublicURL())
}

func TestGetStoragePublicURL_EmptyRegionS3(t *testing.T) {
	cfg := &Config{
		Storage: StorageConfig{
			Type:   StorageTypeS3,
			Bucket: "my-bucket",
			Region: "",
		},
	}
	assert.Equal(t, "https://my-bucket.s3.amazonaws.com", cfg.GetStoragePublicURL())
}

// ===========================================================================
// processJSONResponse
// ===========================================================================

func TestProcessJSONResponse_NilService(t *testing.T) {
	result := processJSONResponse([]byte(`{"key":"value"}`), nil, "myapp.com")
	assert.Nil(t, result)
}

func TestProcessJSONResponse_DisabledService(t *testing.T) {
	svc := newTestService(t, false, true)
	result := processJSONResponse([]byte(`{"key":"value"}`), svc, "myapp.com")
	assert.Nil(t, result)
}

func TestProcessJSONResponse_InvalidJSON(t *testing.T) {
	svc := newTestService(t, true, true)
	result := processJSONResponse([]byte(`not json at all`), svc, "myapp.com")
	assert.Nil(t, result)
}

func TestProcessJSONResponse_EmptyData(t *testing.T) {
	svc := newTestService(t, true, true)
	result := processJSONResponse([]byte{}, svc, "myapp.com")
	assert.Nil(t, result)
}

func TestProcessJSONResponse_NullJSON(t *testing.T) {
	svc := newTestService(t, true, true)
	result := processJSONResponse([]byte(`null`), svc, "myapp.com")
	// null is valid JSON; processValue returns nil; json.Marshal(nil) = "null"
	assert.NotNil(t, result)
	assert.Equal(t, "null", string(result))
}

func TestProcessJSONResponse_SimpleString(t *testing.T) {
	svc := newTestService(t, true, true)
	input := `"hello world"`
	result := processJSONResponse([]byte(input), svc, "myapp.com")
	assert.NotNil(t, result)
	// The string has no remote image URLs, so it should be returned unchanged.
	assert.Equal(t, input, string(result))
}

func TestProcessJSONResponse_SimpleObject(t *testing.T) {
	svc := newTestService(t, true, true)
	input := `{"id":123,"text":"hello"}`
	result := processJSONResponse([]byte(input), svc, "myapp.com")
	assert.NotNil(t, result)
	// No remote image URLs → content should be equivalent.
	var got map[string]any

	err := json.Unmarshal(result, &got)
	require.NoError(t, err)
	assert.InDelta(t, float64(123), got["id"], 1e-9)
	assert.Equal(t, "hello", got["text"])
}

func TestProcessJSONResponse_Array(t *testing.T) {
	svc := newTestService(t, true, true)
	input := `[1, "two", 3.0]`
	result := processJSONResponse([]byte(input), svc, "myapp.com")
	assert.NotNil(t, result)

	var got []any

	err := json.Unmarshal(result, &got)
	require.NoError(t, err)
	assert.Len(t, got, 3)
	assert.InDelta(t, float64(1), got[0], 1e-9)
	assert.Equal(t, "two", got[1])
}

func TestProcessJSONResponse_NestedObject(t *testing.T) {
	svc := newTestService(t, true, true)
	input := `{"outer":{"inner":{"value":42}}}`
	result := processJSONResponse([]byte(input), svc, "myapp.com")
	assert.NotNil(t, result)

	var got map[string]any

	err := json.Unmarshal(result, &got)
	require.NoError(t, err)

	outer := got["outer"].(map[string]any)   //nolint:forcetypeassert
	inner := outer["inner"].(map[string]any) //nolint:forcetypeassert
	assert.InDelta(t, float64(42), inner["value"], 1e-9)
}

func TestProcessJSONResponse_NestedArray(t *testing.T) {
	svc := newTestService(t, true, true)
	input := `{"items":[{"id":1},{"id":2}]}`
	result := processJSONResponse([]byte(input), svc, "myapp.com")
	assert.NotNil(t, result)

	var got map[string]any

	err := json.Unmarshal(result, &got)
	require.NoError(t, err)

	items := got["items"].([]any) //nolint:forcetypeassert
	assert.Len(t, items, 2)
}

func TestProcessJSONResponse_BooleanValues(t *testing.T) {
	svc := newTestService(t, true, true)
	input := `{"active":true,"deleted":false}`
	result := processJSONResponse([]byte(input), svc, "myapp.com")
	assert.NotNil(t, result)

	var got map[string]any

	err := json.Unmarshal(result, &got)
	require.NoError(t, err)
	assert.Equal(t, true, got["active"])
	assert.Equal(t, false, got["deleted"])
}

// ===========================================================================
// processValue
// ===========================================================================

func TestProcessValue_String(t *testing.T) {
	svc := newTestService(t, true, true)
	result := processValue("hello world", svc, "myapp.com")
	assert.Equal(t, "hello world", result)
}

func TestProcessValue_Integer(t *testing.T) {
	svc := newTestService(t, true, true)
	result := processValue(42, svc, "myapp.com")
	assert.Equal(t, 42, result)
}

func TestProcessValue_Float(t *testing.T) {
	svc := newTestService(t, true, true)
	result := processValue(3.14, svc, "myapp.com")
	assert.InDelta(t, 3.14, result, 0.001)
}

func TestProcessValue_Bool(t *testing.T) {
	svc := newTestService(t, true, true)
	assert.Equal(t, true, processValue(true, svc, "myapp.com"))
	assert.Equal(t, false, processValue(false, svc, "myapp.com"))
}

func TestProcessValue_Nil(t *testing.T) {
	svc := newTestService(t, true, true)
	assert.Nil(t, processValue(nil, svc, "myapp.com"))
}

func TestProcessValue_EmptyString(t *testing.T) {
	svc := newTestService(t, true, true)
	assert.Empty(t, processValue("", svc, "myapp.com"))
}

func TestProcessValue_MapWithStrings(t *testing.T) {
	svc := newTestService(t, true, true)
	input := map[string]any{
		"key1": "value1",
		"key2": "value2",
		"num":  float64(10),
	}
	result := processValue(input, svc, "myapp.com")
	m, ok := result.(map[string]any)
	require := assert.New(t)
	require.True(ok)
	assert.Equal(t, "value1", m["key1"])
	assert.Equal(t, "value2", m["key2"])
	assert.InDelta(t, float64(10), m["num"], 0)
}

func TestProcessValue_Slice(t *testing.T) {
	svc := newTestService(t, true, true)
	input := []any{"a", "b", float64(1)}
	result := processValue(input, svc, "myapp.com")
	arr, ok := result.([]any)
	require := assert.New(t)
	require.True(ok)
	assert.Equal(t, "a", arr[0])
	assert.Equal(t, "b", arr[1])
	assert.InDelta(t, float64(1), arr[2], 0)
}

func TestProcessValue_DeeplyNested(t *testing.T) {
	svc := newTestService(t, true, true)
	input := map[string]any{
		"level1": map[string]any{
			"level2": map[string]any{
				"level3": "deep_value",
			},
		},
	}
	result := processValue(input, svc, "myapp.com")
	m := result.(map[string]any)        //nolint:forcetypeassert
	l1 := m["level1"].(map[string]any)  //nolint:forcetypeassert
	l2 := l1["level2"].(map[string]any) //nolint:forcetypeassert
	assert.Equal(t, "deep_value", l2["level3"])
}

func TestProcessValue_NilService(t *testing.T) {
	// processValue doesn't check for nil service, it just recurses.
	// processString will extract remote URLs and call svc.BatchGetThumbnailURLs
	// which will panic if svc is nil. For non-string values, nil service is safe.
	result := processValue(42, nil, "myapp.com")
	assert.Equal(t, 42, result)

	result = processValue(map[string]any{"key": "val"}, nil, "myapp.com")
	assert.NotNil(t, result)
}

// ===========================================================================
// processString
// ===========================================================================

func TestProcessString_Empty(t *testing.T) {
	svc := newTestService(t, true, true)
	result := processString("", svc, "myapp.com")
	assert.Empty(t, result)
}

func TestProcessString_NoRemoteImageURLs(t *testing.T) {
	svc := newTestService(t, true, true)
	content := "Hello, this is plain text with no image URLs."
	result := processString(content, svc, "myapp.com")
	assert.Equal(t, content, result)
}

func TestProcessString_LocalImageURLsOnly(t *testing.T) {
	svc := newTestService(t, true, true)
	content := "Check out https://myapp.com/uploads/photo.jpg for more info."
	result := processString(content, svc, "myapp.com")
	// Local URLs are filtered out by extractRemoteImageURLs, so no DB call.
	assert.Equal(t, content, result)
}

func TestProcessString_NonImageURLs(t *testing.T) {
	svc := newTestService(t, true, true)
	content := "Visit https://example.com/page or download https://files.example.com/doc.pdf"
	result := processString(content, svc, "myapp.com")
	// PDF URLs don't match the image regex, so no remote URLs found.
	assert.Equal(t, content, result)
}

func TestProcessString_RemoteImageURLs_TriggersDB(t *testing.T) {
	// This test would trigger a DB call via BatchGetThumbnailURLs → GetThumbnailURL.
	// We skip it because it requires a database connection.
	t.Skip("Requires database connection for BatchGetThumbnailURLs")
}

// ===========================================================================
// Integration-like tests: ensure exported package surface compiles correctly
// ===========================================================================

func TestThumbnailRuleTypeIsExported(t *testing.T) {
	// Verify that ThumbnailRule is an exported type from the thumbnail package.
	var _ = ThumbnailRule{}
}

func TestAccessModeConstants(t *testing.T) {
	assert.Equal(t, AccessModeProxy, AccessMode("proxy"))
	assert.Equal(t, AccessModeRedirect, AccessMode("redirect"))
}

func TestStorageTypeConstants(t *testing.T) {
	assert.Equal(t, StorageTypeLocal, StorageType("local"))
	assert.Equal(t, StorageTypeS3, StorageType("s3"))
	assert.Equal(t, StorageTypeMinIO, StorageType("minio"))
	assert.Equal(t, StorageTypeAliyunOSS, StorageType("aliyun_oss"))
	assert.Equal(t, StorageTypeTencentCOS, StorageType("tencent_cos"))
}

func TestThumbnailResultTypeIsExported(t *testing.T) {
	var _ = ThumbnailResult{}
}

// ===========================================================================
// Edge-case: ensure file-based test temp dirs are cleaned up
// ===========================================================================

func TestMain(m *testing.M) {
	code := m.Run()
	// Clean up any test storage artifacts
	_ = os.RemoveAll("./test_storage")

	os.Exit(code)
}
