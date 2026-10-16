package admin

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"github.com/kasuganosora/community/systemsettings"
)

// EnvImportItem is one .env line that maps to an admin-editable setting.
type EnvImportItem struct {
	Env    string
	Key    string
	Value  any
	Secret bool
	Err    error
}

// PlanEnvImport lists the settings in envFile that can live in the database
// instead. Core settings (database, Redis, domain, secrets used for
// encryption, listen address) have no admin field and are never included.
func PlanEnvImport(envFile string) ([]EnvImportItem, error) {
	values, err := godotenv.Read(envFile)
	if err != nil {
		return nil, err
	}
	var items []EnvImportItem
	seen := map[string]bool{}
	for _, section := range settingsSections() {
		for _, field := range section.Fields {
			if field.Env == "" || seen[field.Env] {
				continue
			}
			raw, ok := values[field.Env]
			if !ok {
				continue
			}
			seen[field.Env] = true
			item := EnvImportItem{Env: field.Env, Key: field.Key, Secret: field.Type == "secret"}
			item.Value, item.Err = convertEnvValue(field, strings.TrimSpace(raw))
			if item.Err == nil {
				item.Err = validateSettingsValue(field, item.Value)
			}
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Env < items[j].Env })
	return items, nil
}

func convertEnvValue(field SettingsField, raw string) (any, error) {
	switch field.Type {
	case "boolean":
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("%s is not a boolean", field.Env)
		}
		return v, nil
	case "number":
		v, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("%s is not a number", field.Env)
		}
		return float64(v), nil
	}
	return raw, nil
}

// ApplyEnvImport saves the valid items in one transaction.
func ApplyEnvImport(items []EnvImportItem) error {
	values := map[string]any{}
	for _, item := range items {
		if item.Err == nil {
			values[item.Key] = item.Value
		}
	}
	if len(values) == 0 {
		return nil
	}
	return systemsettings.SaveChanges(values)
}

// ShadowedByProcessEnv returns the variable names of admin-editable settings
// that are stored in the database but still set in the process environment,
// where they keep overriding the stored value. Compose env_file entries end
// up in the process environment, so removing a line from /app/config/.env
// alone is not enough. stored reports whether a settings key has a database
// value.
func ShadowedByProcessEnv(lookup func(string) (string, bool), stored func(string) bool) []string {
	var names []string
	seen := map[string]bool{}
	for _, section := range settingsSections() {
		for _, field := range section.Fields {
			if field.Env == "" || seen[field.Env] {
				continue
			}
			seen[field.Env] = true
			if v, ok := lookup(field.Env); ok && strings.TrimSpace(v) != "" && stored(field.Key) {
				names = append(names, field.Env)
			}
		}
	}
	sort.Strings(names)
	return names
}

// StoredSettingKey reports whether key has a value in system settings.
func StoredSettingKey(key string) bool {
	systemsettings.Load()
	_, ok := systemsettings.GetAllFlatStyle()[key]
	return ok
}
