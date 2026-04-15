# C2 & C3 Fix Proposal Analysis — ActivityPub Signature Verification

## Codebase Summary

### Current Verification Flow (`signature.go:212-252`)
```
VerifyRequestWithKeyID(req, body):
  1. Parse Signature header → sig (with sig.Headers list)
  2. Fetch public key (cache or remote fetch)
  3. [OPTIONAL] verifyDigest if Digest header present AND body != nil
  4. Build signing string from sig.Headers
  5. Verify RSA signature
  return nil
```

### Outgoing Signature (`client.go:96-136`)
The **self-server** signs outgoing requests with these headers:
```
headers="(request-target) host date digest"
Date format: http.TimeFormat → "Mon, 02 Jan 2006 15:04:05 GMT"
Digest: SHA-256=<base64(body-hash)>
```

### Inbox Dedup (`inbox.go:80-90`)
```go
// After VerifyActivitySignature succeeds:
activityID, _ := activity["id"].(string)
if activityID != "" {
    // Check ProcessedActivity table → skip if already seen
    db.GetDB().Create(&ProcessedActivity{ActivityID: activityID})
}
```

### ProcessedActivity Model (`deliver.go:44-52`)
```go
type ProcessedActivity struct {
    ID         uint64
    ActivityID string `gorm:"uniqueIndex"`
    CreatedAt  *time.Time `gorm:"autoCreateTime"`
}
```

---

## C2: ActivityPub Replay Attack — No Date Validation

### The Vulnerability

**`VerifyRequestWithKeyID` never checks the Date header for freshness.** The Date header is included in the signing string (by the signing party), but the verifier never validates its value. An attacker can:

1. Capture a valid signed request (e.g., a `Delete` activity)
2. Re-transmit it days, weeks, or years later
3. The RSA signature still verifies (it was valid when originally signed)
4. The activity gets processed as if new

**Existing mitigation is insufficient:**
- The `ProcessedActivity` dedup in `inbox.go:80-90` only helps if:
  - The activity has an `id` field
  - The `ProcessedActivity` table hasn't been cleared/pruned
  - The activity ID matches exactly
- Activities WITHOUT an `id` field (e.g., malformed or edge-case activities) have zero replay protection
- A replay of a `Delete` activity still succeeds on FIRST delivery before dedup kicks in
- The dedup runs AFTER signature verification, wasting CPU on the RSA operation for replayed requests

### Date Format Analysis

Go provides several date format constants:

| Format | Pattern | Example |
|--------|---------|---------|
| `http.TimeFormat` | `Mon, 02 Jan 2006 15:04:05 GMT` | Always "GMT" suffix |
| `time.RFC1123` | `Mon, 02 Jan 2006 15:04:05 MST` | Timezone name (GMT, UTC, EST, etc.) |
| `time.RFC1123Z` | `Mon, 02 Jan 2006 15:04:05 -0700` | Numeric offset |

**Critical compatibility facts:**
- `time.Parse(time.RFC1123, "... GMT")` → ✅ works (GMT is a recognized tz abbreviation)
- `time.Parse(time.RFC1123, "... UTC")` → ✅ works
- `time.Parse(time.RFC1123, "... -0500")` → ❌ FAILS (expects tz name, not number)
- `time.Parse(time.RFC1123Z, "... GMT")` → ❌ FAILS (expects numeric, not name)
- `time.Parse(time.RFC1123Z, "... -0500")` → ✅ works
- `http.ParseTime(...)` → ✅ handles ALL standard HTTP date formats (RFC1123, RFC1123Z, RFC850, asctime, HTTP TimeFormat)

**Self-server outgoing:** Uses `http.TimeFormat` (always GMT). Parseable by `time.RFC1123`.

**External servers (Mastodon, Pleroma, Misskey, etc.):** May use any valid HTTP date format. Some use RFC1123 with numeric offsets.

**Recommendation:** Use `http.ParseTime()` — it's a single call that handles every valid HTTP date format. No fallback chain needed.

### Clock Skew Analysis

| Implementation | Tolerance | Notes |
|---------------|-----------|-------|
| Mastodon | ~30 seconds | Aggressive but effective |
| Pleroma/Akkoma | 5 minutes | Standard |
| Lemmy | 5 minutes | Standard |
| GoToSocial | 5 minutes | Standard |
| HTTP Sig spec (draft) | No mandatory value | Delegates to implementations |

**Recommendation: 5 minutes.**
- Rationale: 30 seconds is too aggressive for the fediverse where server clocks can drift significantly (especially self-hosted instances). 5 minutes is the de facto standard and balances security with interoperability. The window can be made configurable via a constant or system setting.

### Missing Date Header

If Date header is absent:
- **Must reject.** Without a Date, there's no way to check freshness.
- Additionally, must verify that `date` is in `sig.Headers`. If `date` is NOT in the signed headers list, the Date value is unauthenticated — an attacker could set it to anything.

### Proposed Fix (C2)

```go
// verifyRequestDate validates the Date header for replay protection
// Checks: Date is present, Date is in signed headers, Date is within clock skew
func verifyRequestDate(req *http.Request, signedHeaders []string) error {
    dateStr := req.Header.Get("Date")
    if dateStr == "" {
        return fmt.Errorf("missing Date header")
    }

    // Verify date is in signed headers (otherwise it's unauthenticated)
    dateIsSigned := false
    for _, h := range signedHeaders {
        if strings.ToLower(h) == "date" {
            dateIsSigned = true
            break
        }
    }
    if !dateIsSigned {
        return fmt.Errorf("date header is not signed; cannot verify freshness")
    }

    // Parse the date using http.ParseTime which handles all HTTP date formats
    date, err := http.ParseTime(dateStr)
    if err != nil {
        return fmt.Errorf("invalid Date header format: %w", err)
    }

    // Check clock skew (5 minutes tolerance)
    now := time.Now().UTC()
    skew := 5 * time.Minute
    if date.Before(now.Add(-skew)) || date.After(now.Add(skew)) {
        return fmt.Errorf("request date %s is outside acceptable clock skew (±5 minutes)", dateStr)
    }

    return nil
}
```

**Integration point in `VerifyRequestWithKeyID`:** Insert after `ParseSignatureHeader` (line 220), BEFORE `getPublicKey` and crypto operations — fail fast on replay attacks without spending CPU or network on key fetches:

```go
// Insert after line 220 (after ParseSignatureHeader succeeds):
if err := verifyRequestDate(req, sig.Headers); err != nil {
    return fmt.Errorf("date verification failed: %w", err)
}
```

### C2 Feasibility Assessment

| Dimension | Rating | Notes |
|-----------|--------|-------|
| **Feasibility** | ✅ **High** | Pure addition — no existing code modified. `http.ParseTime` is stdlib. The check is a simple time comparison. No database or network calls. |
| **Stability** | ⚠️ **Medium** | Risk of false positives on servers with severely misconfigured clocks (>5min drift). Mitigated by being the same window most implementations use. Could make configurable. |
| **Maintainability** | ✅ **High** | Self-contained function, clear semantics, uses stdlib only. No coupling to other components. Easy to adjust clock skew or add logging. |

**Edge cases to test:**
- Date exactly at ±5 minute boundary
- Date far in the past (1 hour, 1 day)
- Date far in the future
- Missing Date header
- Date present but not in signed headers
- Various date formats (GMT, UTC, +0000, -0500)
- Malformed date strings

---

## C3: Digest Not Enforced — Body Tampering

### The Vulnerability

**`VerifyRequestWithKeyID` line 230-234:**
```go
// Verify digest if present
if digest := req.Header.Get("Digest"); digest != "" && body != nil {
    if err := verifyDigest(body, digest); err != nil {
        return fmt.Errorf("digest verification failed: %w", err)
    }
}
```

This check is **completely optional**. An attacker can defeat body integrity by:

**Attack vector 1: Omit Digest header entirely**
1. Sign request with only `(request-target) host date` — no `digest` in signed headers
2. Set any arbitrary body (malicious activity JSON)
3. No Digest header is present → `digest != ""` is false → verification skipped entirely
4. RSA signature verifies because digest was never part of the signed data
5. The malicious body is processed

**Attack vector 2: Include Digest but don't sign it**
1. Sign request with `(request-target) host date` — no `digest` in signed headers
2. Set malicious body AND matching Digest header (attacker computes both)
3. Digest verification passes (body matches digest)
4. But the Digest header is NOT covered by the signature, so the attacker could replay with a DIFFERENT body AND digest and the signature would still verify
5. This is less practical (requires intercepting/modifying in flight) but still a spec violation

### Root Cause
The code trusts the Digest header value even when it's not in the signed headers list. Per the HTTP Signatures spec, only headers listed in the `headers` parameter are authenticated. If `digest` isn't in `sig.Headers`, its value cannot be trusted.

### GET Requests with No Body
ActivityPub inbox endpoints are POST-only (per the spec and the `Inbox()` handler). However, to be robust:
- GET/HEAD requests with no body should NOT require digest
- POST/PUT/PATCH requests with a body MUST have digest in signed headers
- The body length check `len(body) > 0` distinguishes these cases

### Proposed Fix (C3)

```go
// verifyDigestIsSigned ensures body integrity by requiring the digest
// header to be present and signed for requests with a body
func verifyDigestIsSigned(req *http.Request, signedHeaders []string, body []byte) error {
    // No body → no digest needed
    if body == nil || len(body) == 0 {
        return nil
    }

    // Check that digest is in the signed headers
    digestIsSigned := false
    for _, h := range signedHeaders {
        if strings.ToLower(h) == "digest" {
            digestIsSigned = true
            break
        }
    }
    if !digestIsSigned {
        return fmt.Errorf("digest header must be included in signed headers for requests with a body")
    }

    // Verify the Digest header is present
    digestHeader := req.Header.Get("Digest")
    if digestHeader == "" {
        return fmt.Errorf("digest header is required for requests with a body")
    }

    // Verify body matches digest
    if err := verifyDigest(body, digestHeader); err != nil {
        return fmt.Errorf("digest verification failed: %w", err)
    }

    return nil
}
```

**Integration point in `VerifyRequestWithKeyID`:** Replace lines 229-234 with:

```go
// Replace existing optional digest check (lines 229-234) with:
if err := verifyDigestIsSigned(req, sig.Headers, body); err != nil {
    return err
}
```

### C3 Feasibility Assessment

| Dimension | Rating | Notes |
|-----------|--------|-------|
| **Feasibility** | ✅ **High** | Replaces existing optional check with mandatory one. Logic is straightforward. No new dependencies. |
| **Stability** | ⚠️ **Medium** | Risk of breaking federation with non-conformant implementations that don't sign the digest header. However, the HTTP Signatures spec (Cavage) REQUIRES digest to be signed for requests with bodies. Most major implementations (Mastodon, Pleroma, Misskey) already sign digest. The self-server itself always signs digest. |
| **Maintainability** | ✅ **High** | Single clear function, replaces existing inline check. Clear error messages for debugging interop issues. |

**Edge cases to test:**
- POST with body, digest signed, correct digest → ✅ pass
- POST with body, digest NOT in signed headers → ❌ reject
- POST with body, no Digest header → ❌ reject
- POST with body, digest signed, WRONG digest value → ❌ reject
- POST with empty body → ✅ pass (no digest needed)
- GET with no body → ✅ pass (no digest needed)
- Body is nil → ✅ pass

---

## Cross-Analysis: C2 and C3 Interaction

### Ordering in Verification Pipeline

The current flow and proposed insertion points:

```
1. ParseSignatureHeader(sigHeader)          → [existing] get sig.Headers
2. verifyRequestDate(req, sig.Headers)      → [C2] NEW — date freshness
3. getPublicKey(ctx, sig.KeyID)             → [existing] fetch/lookup key
4. verifyDigestIsSigned(req, sig.Headers, body) → [C3] NEW — body integrity
5. BuildSigningString(req, sig.Headers)     → [existing]
6. VerifyWithRSA(publicKey, signingString, signature) → [existing]
```

**Why this order matters:**

1. **C2 before C3 and signature verification (cheap checks first):**
   - Date check is a simple string parse + time comparison — ~microseconds
   - Rejecting replayed requests BEFORE key fetches (network I/O) and RSA verification (CPU-intensive) saves significant resources
   - This is a defense-in-depth ordering: don't waste resources on attacks

2. **C3 before signature verification (fail fast on body tampering):**
   - If digest isn't signed, no point verifying the RSA signature — the body is untrusted regardless
   - The digest verification is cheaper than RSA (SHA-256 vs RSA-2048)
   - Prevents processing of requests that would verify cryptographically but have tampered bodies

3. **Both C2 and C3 require `sig.Headers`:**
   - C2 checks if `date` ∈ `sig.Headers` (otherwise Date is unauthenticated)
   - C3 checks if `digest` ∈ `sig.Headers` (otherwise Digest is unauthenticated)
   - Both must happen AFTER `ParseSignatureHeader` but BEFORE crypto operations

4. **Signature verification is last (expensive, but proves provenance):**
   - Only after all preconditions (freshness, body integrity) pass
   - RSA-2048 verification is the most expensive operation (~100μs)
   - Ensures we don't waste CPU on requests we'll ultimately reject

### Potential Interaction Issues

**No ordering conflicts detected.** The two fixes are orthogonal:
- C2 validates temporal freshness (Date header)
- C3 validates body integrity (Digest header)
- They check different headers in `sig.Headers`
- They don't depend on each other's results
- Both can fail independently

**Shared concern — `sig.Headers` validation:**
Currently, `sig.Headers` could be empty or contain unexpected values. Consider adding a minimum header validation:
```go
// Require at minimum: (request-target) and date
requiredHeaders := []string{"(request-target)", "date"}
for _, required := range requiredHeaders {
    found := false
    for _, h := range sig.Headers {
        if strings.ToLower(h) == required {
            found = true
            break
        }
    }
    if !found {
        return fmt.Errorf("required signed header '%s' is missing", required)
    }
}
```

### Recommended Combined Fix Location

In `VerifyRequestWithKeyID`, the full proposed change:

```go
func (v *SignatureVerifier) VerifyRequestWithKeyID(req *http.Request, body []byte) error {
    sigHeader := req.Header.Get("Signature")
    if sigHeader == "" {
        return fmt.Errorf("no signature header")
    }

    sig, err := ParseSignatureHeader(sigHeader)
    if err != nil {
        return fmt.Errorf("parsing signature header: %w", err)
    }

    // === NEW: C2 — Replay protection via Date validation ===
    if err := verifyRequestDate(req, sig.Headers); err != nil {
        return fmt.Errorf("date verification failed: %w", err)
    }

    // Get public key (from cache or fetch)
    publicKey, err := v.getPublicKey(req.Context(), sig.KeyID)
    if err != nil {
        return fmt.Errorf("getting public key: %w", err)
    }

    // === NEW: C3 — Body integrity via Digest enforcement ===
    if err := verifyDigestIsSigned(req, sig.Headers, body); err != nil {
        return err  // error already descriptive
    }

    // Build and verify signature
    signingString, err := BuildSigningString(req, sig.Headers)
    if err != nil {
        return fmt.Errorf("building signing string: %w", err)
    }

    signature, err := base64.StdEncoding.DecodeString(sig.Signature)
    if err != nil {
        return fmt.Errorf("decoding signature: %w", err)
    }

    if err := VerifyWithRSA(publicKey, []byte(signingString), signature); err != nil {
        return fmt.Errorf("signature verification failed: %w", err)
    }

    return nil
}
```

---

## Summary Table

| | C2 (Replay Protection) | C3 (Digest Enforcement) |
|---|---|---|
| **Severity** | High — allows indefinite replay | Critical — allows body tampering |
| **Fix complexity** | Low (~25 lines) | Low (~20 lines) |
| **Feasibility** | ✅ High | ✅ High |
| **Stability** | ⚠️ Medium (clock skew edge cases) | ⚠️ Medium (interop with non-conformant servers) |
| **Maintainability** | ✅ High (self-contained, stdlib) | ✅ High (self-contained, stdlib) |
| **Risk if unfixed** | Replay attacks, resource exhaustion | Body tampering, forged activities |
| **Dependencies** | None (stdlib `net/http`) | None (stdlib `crypto/sha256`) |
| **Interaction with other fix** | No conflicts; complementary | No conflicts; complementary |
| **Insertion point** | After ParseSignatureHeader, before getPublicKey | After getPublicKey, before BuildSigningString |
| **Test cases needed** | 7 (see above) | 7 (see above) |