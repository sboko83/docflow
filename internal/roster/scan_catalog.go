package roster

import (
	"path/filepath"
	"time"
)

// ScanOptions configures one catalog scan. The zero value is the offline
// scan of the default cache path: the local scanners read the tools on
// this machine, the auth probes ask them, and models.dev is not touched.
type ScanOptions struct {
	// CachePath overrides the catalog cache ("" means DefaultCachePath).
	CachePath string
	// Refresh ignores a fresh models.dev cache and fetches it again. The
	// local scanners run on every scan, so the flag changes nothing
	// without Online.
	Refresh bool
	// Online enables the optional models.dev enrichment.
	Online bool
}

// ScanCatalog builds the catalog from every source in merge order — Pi,
// OpenCode, the built-in Codex/Claude table, the auth probes and the
// optional models.dev enrichment — stamps the scan time and writes the
// cache. The warnings are the problems of one source next to a partial
// catalog: a scan never fails because one channel of one access is
// unreadable, only a cache that cannot be written is an error. No probe
// prints or stores a credential.
func ScanCatalog(options ScanOptions) (*Catalog, []string, error) {
	catalog := emptyCatalog()
	var warnings []string
	pi, problems := ScanPi()
	catalog.Merge(pi)
	warnings = append(warnings, problems...)
	opencode, problems := ScanOpenCode()
	catalog.Merge(opencode)
	warnings = append(warnings, problems...)
	// The built-in table comes before the auth probes: it is what tells
	// them that the codex and claude accesses exist.
	catalog.Merge(BuiltinCatalog())
	warnings = append(warnings, ScanAuth(catalog)...)
	_, problems = EnrichModelsDev(catalog, EnrichOptions{
		Online:    options.Online,
		Refresh:   options.Refresh,
		CachePath: modelsDevCachePathFor(options.CachePath),
	})
	warnings = append(warnings, problems...)
	catalog.ScannedAt = time.Now().UTC().Format(time.RFC3339)
	catalog.Sort()
	if err := catalog.Save(options.CachePath); err != nil {
		return catalog, warnings, err
	}
	return catalog, warnings, nil
}

// modelsDevCachePathFor keeps the models.dev cache next to the catalog
// cache: a custom cache path in a test or a mirror carries both files, and
// the default path stays DefaultModelsDevCachePath.
func modelsDevCachePathFor(catalogPath string) string {
	if catalogPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(expandUser(catalogPath)), "models.dev.json")
}
