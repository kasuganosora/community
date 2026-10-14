package discovery

// This opt-in evaluator runs production extraction, retrieval, selection and
// ranking. It never treats pipeline completion as a recommendation quality pass.
// Relevance labels and scenario descriptions are deliberately absent from the
// input structs below: they are consumed only by the offline Python evaluator.
//
// Run from the repository root with an isolated real Meilisearch service:
// DISCOVERY_TEST_MEILI_QUALITY=1 DISCOVERY_TEST_MEILI_URL=http://127.0.0.1:17700 \
//   go test ./discovery -run '^TestMeiliQualityEvaluation$' -count=1 -v
// python3 scripts/recommendation/evaluate_meili_quality.py
//
// Optional real hybrid evaluation also requires DISCOVERY_TEST_EMBEDDER_CONFIG_FILE
// (a local Meili embedder JSON file) and DISCOVERY_TEST_EMBEDDER_MODEL_VERSION.
// Only explicit opt-in reaches the external engine; ordinary tests skip this.
// Runtime artifacts default to /tmp and never contain embedder configuration.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type qualityFixture struct {
	Version string        `json:"fixture_id"`
	Seed    int64         `json:"seed"`
	Posts   []qualityPost `json:"documents"`
	Users   []qualityUser `json:"users"`
}
type qualityPost struct {
	ID         string   `json:"id"`
	AuthorID   string   `json:"author"`
	Text       string   `json:"text"`
	Tags       []string `json:"tags"`
	Language   string   `json:"language"`
	AgeHours   float64  `json:"age_hours"`
	Visibility string   `json:"visibility"`
}
type qualityUser struct {
	ID             string   `json:"id"`
	AccountAgeDays int      `json:"account_age_days"`
	FollowedTags   []string `json:"topics"`
	Languages      []string `json:"reading_languages"`
	Personalized   bool     `json:"-"`
}
type qualityModeResult struct {
	Mode         string                         `json:"mode"`
	CandidateIDs []string                       `json:"candidate_ids"`
	InterestIDs  []string                       `json:"interest_ids"`
	RankIDs      []string                       `json:"rank_ids"`
	FixedRankIDs []string                       `json:"fixed_rank_ids"`
	Families     map[string]map[string][]string `json:"families"`
	Milliseconds float64                        `json:"selection_and_ranking_ms"`
}
type qualityRun struct {
	FixtureSHA256         string                                  `json:"fixture_sha256"`
	FixtureVersion        string                                  `json:"fixture_version"`
	Seed                  int64                                   `json:"seed"`
	AsOf                  time.Time                               `json:"as_of"`
	Algorithm             string                                  `json:"algorithm"`
	EmbeddingModelVersion string                                  `json:"embedding_model_version,omitempty"`
	CodeSHA256            map[string]string                       `json:"code_sha256"`
	EligibleIDs           []string                                `json:"eligible_ids"`
	Versions              map[string]MeiliStatus                  `json:"versions"`
	BuildMS               map[string]float64                      `json:"build_ms"`
	RetrievalMS           map[string]float64                      `json:"retrieval_ms"`
	NotRun                map[string]string                       `json:"not_run"`
	Users                 map[string]map[string]qualityModeResult `json:"users"`
}

func TestMeiliQualityEvaluation(t *testing.T) {
	if os.Getenv("DISCOVERY_TEST_MEILI_QUALITY") != "1" {
		t.Skip("opt in with DISCOVERY_TEST_MEILI_QUALITY=1; writes evaluation evidence, not a release approval")
	}
	host := strings.TrimRight(os.Getenv("DISCOVERY_TEST_MEILI_URL"), "/")
	require.NotEmpty(t, host, "an actual Meilisearch URL is required")
	fixturePath := os.Getenv("DISCOVERY_TEST_QUALITY_FIXTURE")
	if fixturePath == "" {
		fixturePath = "../docs/testdata/recommendation/meili-quality-v1.json"
	}
	raw, err := os.ReadFile(fixturePath)
	require.NoError(t, err)
	digest := sha256.Sum256(raw)
	expected, err := os.ReadFile(fixturePath + ".sha256")
	require.NoError(t, err)
	checksumFields := strings.Fields(string(expected))
	require.NotEmpty(t, checksumFields, "frozen fixture checksum is empty")
	require.Equal(t, checksumFields[0], hex.EncodeToString(digest[:]), "frozen fixture checksum changed")
	var fixture qualityFixture
	require.NoError(t, json.Unmarshal(raw, &fixture))
	require.NotEmpty(t, fixture.Posts)
	require.NotEmpty(t, fixture.Users)
	for i := range fixture.Users {
		fixture.Users[i].Personalized = len(fixture.Users[i].FollowedTags) > 0
	}
	cutoff := time.Now().UTC().Truncate(time.Second)
	conn, viewers, postIDs := qualityDatabase(t, fixture, cutoff)
	ctx := context.Background()
	var statuses models.Statuses
	require.NoError(t, models.RecommendationStatusScope(conn, cutoff, CandidateWindow).Order("statuses.created_at DESC, statuses.id DESC").Find(&statuses).Error)
	statuses, err = models.FilterRecommendableStatuses(conn, nil, statuses, models.RecommendationEligibilityOptions{Context: "public", AsOf: cutoff, Window: CandidateWindow, RootWindow: true, MetadataOnly: true})
	require.NoError(t, err)
	metadata, err := candidateMetadata(conn, statuses)
	require.NoError(t, err)
	sort.Slice(metadata, func(i, j int) bool {
		if metadata[i].CreatedAt.Equal(metadata[j].CreatedAt) {
			return metadata[i].StatusID > metadata[j].StatusID
		}
		return metadata[i].CreatedAt.After(metadata[j].CreatedAt)
	})
	toIDs := func(ids []uint64) []string {
		out := make([]string, 0, len(ids))
		for _, id := range ids {
			name, ok := postIDs[id]
			require.True(t, ok, "unknown returned status %d", id)
			out = append(out, name)
		}
		return out
	}
	result := qualityRun{FixtureSHA256: hex.EncodeToString(digest[:]), FixtureVersion: fixture.Version, Seed: fixture.Seed, AsOf: cutoff, Algorithm: AlgorithmVersion, CodeSHA256: map[string]string{}, Versions: map[string]MeiliStatus{}, BuildMS: map[string]float64{}, RetrievalMS: map[string]float64{}, NotRun: map[string]string{}, Users: map[string]map[string]qualityModeResult{}}
	for _, file := range []string{"candidates.go", "rank.go", "meili_provider.go", "meili_provider_budget.go"} {
		b, e := os.ReadFile(file)
		require.NoError(t, e)
		h := sha256.Sum256(b)
		result.CodeSHA256[file] = hex.EncodeToString(h[:])
	}
	for _, candidate := range metadata {
		result.EligibleIDs = append(result.EligibleIDs, postIDs[candidate.StatusID])
	}
	output := os.Getenv("DISCOVERY_TEST_QUALITY_OUTPUT")
	if output == "" {
		output = filepath.Join(os.TempDir(), "community-meili-quality.json")
	}
	write := func() {
		b, e := json.MarshalIndent(result, "", "  ")
		require.NoError(t, e)
		require.NoError(t, os.WriteFile(output, append(b, '\n'), 0600))
	}
	defer write()
	pools := map[string]candidatePool{"rules": {Generation: "quality-rules", AsOf: cutoff, Mode: "rules", Candidates: metadata}}
	for _, mode := range []string{"meili_lexical", "meili_hybrid"} {
		p, settings := meiliTestProvider(t, host)
		p.conn = func() *gorm.DB { return conn }
		p.client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		if mode == "meili_hybrid" {
			path := os.Getenv("DISCOVERY_TEST_EMBEDDER_CONFIG_FILE")
			version := os.Getenv("DISCOVERY_TEST_EMBEDDER_MODEL_VERSION")
			if path == "" || version == "" {
				result.NotRun[mode] = "a real versioned embedding model and local configuration file were not supplied"
				continue
			}
			result.EmbeddingModelVersion = version
			configuration, e := os.ReadFile(path)
			require.NoError(t, e)
			require.True(t, json.Valid(configuration))
			p.embedding = &meiliEmbedding{Name: "quality-public-text", ModelVersion: version, Configuration: json.RawMessage(configuration), MaxDocuments: meiliRowLimit, MaxUnitsPerHour: 100000}
		}
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			state, e := p.readState(cleanup)
			if e != nil {
				t.Errorf("read cleanup state: %v", e)
				return
			}
			indexes := []string{state.Pending, state.Retired}
			if state.Active != nil {
				indexes = append(indexes, state.Active.Index)
			}
			for _, index := range indexes {
				if index != "" {
					if e := p.retire(cleanup, index); e != nil {
						t.Errorf("retire own quality index: %v", e)
					}
				}
			}
		})
		started := time.Now()
		err = p.rebuild(ctx)
		result.BuildMS[mode] = float64(time.Since(started).Microseconds()) / 1000
		if err != nil {
			result.NotRun[mode] = "production index build failed; inspect test failure"
			t.Errorf("%s build: %v", mode, err)
			continue
		}
		state, e := p.readState(ctx)
		require.NoError(t, e)
		require.NotNil(t, state.Active)
		require.Equal(t, mode, state.Active.Mode)
		require.Equal(t, len(metadata), state.Active.Documents, "index must contain the same complete eligible fixture set")
		// Approval is confined to the isolated test provider. A production quality
		// gate is never enabled or changed by this evaluation.
		settings.ApprovedVersion = state.Active.Version
		started = time.Now()
		batch, e := p.BuildBuckets(ctx, cutoff, feedActiveBuckets(metadata))
		result.RetrievalMS[mode] = float64(time.Since(started).Microseconds()) / 1000
		if e != nil {
			result.NotRun[mode] = "production bucket query failed; inspect test failure"
			t.Errorf("%s buckets: %v", mode, e)
			continue
		}
		result.Versions[mode] = p.Status()
		pools[mode] = candidatePool{Generation: batch.Generation, AsOf: cutoff, Mode: batch.Mode, Candidates: metadata, Lists: batch.Lists, ContentDataCutoff: batch.DataCutoff}
	}
	for _, user := range fixture.Users {
		viewer := viewers[user.ID]
		result.Users[user.ID] = map[string]qualityModeResult{}
		seed := fmt.Sprintf("quality-%d-%s", fixture.Seed, user.ID)
		for _, mode := range []string{"rules", "meili_lexical", "meili_hybrid"} {
			pool, ok := pools[mode]
			if !ok {
				continue
			}
			started := time.Now()
			candidates, e := selectFeedCandidates(ctx, viewer, user.Personalized, pool, user.Languages)
			require.NoError(t, e)
			blocks := RankCandidates(candidates, cutoff, user.Personalized, seed)
			elapsedMS := float64(time.Since(started).Microseconds()) / 1000
			ids := []uint64{}
			for _, block := range blocks {
				for _, item := range block {
					ids = append(ids, item.ID)
				}
			}
			candidateIDs := []uint64{}
			for _, candidate := range candidates {
				candidateIDs = append(candidateIDs, candidate.StatusID)
			}
			interest := make([]Candidate, 0)
			for _, candidate := range candidates {
				if len(candidate.Sources) > 0 {
					interest = append(interest, candidate)
				}
			}
			sort.Slice(interest, func(i, j int) bool {
				if interest[i].Interest == interest[j].Interest {
					return interest[i].StatusID > interest[j].StatusID
				}
				return interest[i].Interest > interest[j].Interest
			})
			interestIDs := []uint64{}
			for _, candidate := range interest {
				interestIDs = append(interestIDs, candidate.StatusID)
			}
			families := qualityFamilies(t, conn, user, pool)
			fixed := qualityFixedCandidates(metadata, candidates, families, user.Languages)
			fixedIDs := []uint64{}
			for _, block := range RankCandidates(fixed, cutoff, user.Personalized, seed) {
				for _, item := range block {
					fixedIDs = append(fixedIDs, item.ID)
				}
			}
			entry := qualityModeResult{Mode: qualityConsumedMode(mode, user.Personalized, candidates, ids), CandidateIDs: toIDs(candidateIDs), InterestIDs: toIDs(interestIDs), RankIDs: toIDs(ids), FixedRankIDs: toIDs(fixedIDs), Families: map[string]map[string][]string{}, Milliseconds: elapsedMS}
			for family, buckets := range families {
				entry.Families[family] = map[string][]string{}
				for bucket, ids := range buckets {
					entry.Families[family][bucket] = toIDs(ids)
				}
			}
			result.Users[user.ID][mode] = entry
			if len(user.FollowedTags) == 0 {
				require.Equal(t, result.Users[user.ID]["rules"].RankIDs, entry.RankIDs, "cold-start controls must not gain synthetic interests")
			}
		}
		// Independent newest/hot baselines use the same complete eligible set
		// and reading languages. They do not consume labels or sampled negatives.
		for _, baseline := range []string{"newest", "hot"} {
			rows := append([]Candidate(nil), metadata...)
			if baseline == "hot" {
				sort.SliceStable(rows, func(i, j int) bool { return rows[i].Heat > rows[j].Heat })
			}
			ids := []uint64{}
			for _, row := range rows {
				if models.RecommendationLanguageAllowed(row.Language, user.Languages) {
					ids = append(ids, row.StatusID)
				}
			}
			result.Users[user.ID][baseline] = qualityModeResult{Mode: baseline, CandidateIDs: toIDs(ids), RankIDs: toIDs(ids), FixedRankIDs: toIDs(ids), Families: map[string]map[string][]string{}}
		}
	}
	write()
	t.Logf("quality evidence: %s; use scripts/recommendation/evaluate_meili_quality.py for quality gates", output)
}

func qualityDatabase(t *testing.T, fixture qualityFixture, cutoff time.Time) (*gorm.DB, map[string]*models.Account, map[uint64]string) {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true, Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous); _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(&models.User{}, &models.UserSetting{}, &models.Account{}, &models.AccountExtra{}, &models.Status{}, &models.StatusContent{}, &models.Tag{}, &models.StatusTag{}, &models.DomainBlock{}, &models.Block{}, &models.MuteAccount{}, &models.MuteStatus{}, &models.Filter{}, &models.Follow{}, &models.RecommendationFeedback{}, &models.FavoriteStatus{}, &models.StatusReaction{}))
	require.NoError(t, conn.Exec("CREATE TABLE followed_tags (id bigint PRIMARY KEY,account_id bigint,tag_id bigint,created_at timestamp)").Error)
	tx := conn.Session(&gorm.Session{SkipHooks: true})
	accounts := map[string]*models.Account{}
	tags := map[string]uint64{}
	postIDs := map[uint64]string{}
	account := func(name string) *models.Account {
		if a := accounts[name]; a != nil {
			return a
		}
		id := uint64(len(accounts) + 1)
		u := models.User{Id: id, UUID: name, UserName: name, EmailHash: name, Status: models.UserStatusNormal}
		require.NoError(t, tx.Create(&u).Error)
		accountCreated := cutoff.Add(-90 * 24 * time.Hour)
		a := &models.Account{Id: id, UserId: id, Username: name, Acct: name, Discoverable: true, CreatedAt: &accountCreated}
		require.NoError(t, tx.Omit("Extra", "User").Create(a).Error)
		accounts[name] = a
		return a
	}
	tag := func(name string) uint64 {
		key := models.TagKey(name)
		if id := tags[key]; id != 0 {
			return id
		}
		id := uint64(len(tags) + 1)
		require.NoError(t, tx.Create(&models.Tag{Id: id, Name: name, NameKey: key}).Error)
		tags[key] = id
		return id
	}
	statusTagID := uint64(1)
	for i, post := range fixture.Posts {
		id := uint64(i + 1)
		require.NotEmpty(t, post.ID)
		require.NotEmpty(t, post.AuthorID)
		postIDs[id] = post.ID
		a := account("author-" + post.AuthorID)
		created := cutoff.Add(-time.Duration(post.AgeHours * float64(time.Hour)))
		visibility := uint8(models.StatusVisibilityPublic)
		switch post.Visibility {
		case "", "public":
		case "unlisted":
			visibility = models.StatusVisibilityUnlisted
		case "private":
			visibility = models.StatusVisibilityPrivate
		case "direct":
			visibility = models.StatusVisibilityDirect
		default:
			t.Fatalf("unsupported fixture visibility %s", post.Visibility)
		}
		s := &models.Status{Id: id, AccountId: a.Id, Visibility: visibility, CreatedAt: &created, Language: post.Language}
		require.NoError(t, tx.Omit("Content").Create(s).Error)
		require.NoError(t, tx.Create(&models.StatusContent{StatusId: id, Content: post.Text}).Error)
		for _, name := range post.Tags {
			require.NoError(t, tx.Create(&models.StatusTag{Id: statusTagID, StatusId: id, TagId: tag(name)}).Error)
			statusTagID++
		}
	}
	viewers := map[string]*models.Account{}
	followID := 1
	for _, user := range fixture.Users {
		a := account("viewer-" + user.ID)
		created := cutoff.Add(-time.Duration(user.AccountAgeDays) * 24 * time.Hour)
		require.NoError(t, conn.Model(a).UpdateColumn("created_at", created).Error)
		a.CreatedAt = &created
		viewers[user.ID] = a
		for _, name := range user.FollowedTags {
			require.NoError(t, conn.Exec("INSERT INTO followed_tags(id,account_id,tag_id,created_at) VALUES(?,?,?,?)", followID, a.Id, tag(name), cutoff.Add(-time.Hour)).Error)
			followID++
		}
	}
	return conn, viewers, postIDs
}

func qualityFamilies(t *testing.T, conn *gorm.DB, user qualityUser, pool candidatePool) map[string]map[string][]uint64 {
	t.Helper()
	out := map[string]map[string][]uint64{}
	if !user.Personalized {
		return out
	}
	for _, topic := range user.FollowedTags {
		topic = models.TagKey(topic)
		found := false
		for _, list := range pool.Lists {
			if list.BucketKey != topic {
				continue
			}
			found = true
			if out[list.Family] == nil {
				out[list.Family] = map[string][]uint64{}
			}
			out[list.Family][topic] = list.IDs
		}
		if !found {
			var ids []uint64
			require.NoError(t, models.RecommendationStatusScope(conn, pool.AsOf, CandidateWindow).Select("statuses.id").Joins("JOIN status_tags ON status_tags.status_id = statuses.id").Joins("JOIN tags ON tags.id = status_tags.tag_id").Where("tags.name_key = ?", topic).Order("statuses.created_at DESC, statuses.id DESC").Limit(80).Scan(&ids).Error)
			if out["exact"] == nil {
				out["exact"] = map[string][]uint64{}
			}
			out["exact"][topic] = ids
		}
	}
	return out
}

func qualityFixedCandidates(metadata, selected []Candidate, families map[string]map[string][]uint64, languages []string) []Candidate {
	sources := map[uint64][]Source{}
	for _, family := range []string{"exact", "lexical", "semantic"} {
		keys := []string{}
		for key := range families[family] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			for rank, id := range families[family][key] {
				sources[id] = append(sources[id], Source{Family: family, Key: key, Rank: rank + 1})
			}
		}
	}
	explore := map[uint64]bool{}
	for _, row := range selected {
		explore[row.StatusID] = row.Explore
	}
	out := []Candidate{}
	for _, row := range metadata {
		if !models.RecommendationLanguageAllowed(row.Language, languages) {
			continue
		}
		row.Sources = sources[row.StatusID]
		row.Interest = ReciprocalRankFusion(row.Sources)
		row.Explore = explore[row.StatusID]
		out = append(out, row)
	}
	return out
}

func qualityConsumedMode(mode string, personalized bool, candidates []Candidate, ids []uint64) string {
	if !personalized {
		return "rules"
	}
	chosen := map[uint64]bool{}
	for _, id := range ids {
		chosen[id] = true
	}
	consumed := "rules"
	for _, candidate := range candidates {
		if !chosen[candidate.StatusID] {
			continue
		}
		for _, source := range candidate.Sources {
			if source.Family == "semantic" && mode == "meili_hybrid" {
				return "meili_hybrid"
			}
			if source.Family == "lexical" {
				consumed = "meili_lexical"
			}
		}
	}
	return consumed
}
