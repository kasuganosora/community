package local

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	mrand "math/rand/v2"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	Total       = 5
	maxFails    = 4
	startBudget = 30
	lifeTime    = 4 * time.Minute
	tokenLife   = 2 * time.Minute
	rateWindow  = 10 * time.Minute
)

var (
	// ErrRateLimited means this IP opened too many challenges.
	ErrRateLimited = errors.New("captcha rate limited")
	// ErrInvalid means the challenge is missing, expired, or bound to another browser.
	ErrInvalid = errors.New("captcha invalid")
	// ErrEarly means the five characters have not all been shown yet.
	ErrEarly = errors.New("captcha early")
	// ErrWrong means the answer did not match.
	ErrWrong = errors.New("captcha wrong")
	// ErrPow means the browser has not finished the proof-of-work yet.
	ErrPow = errors.New("captcha pow")
)

const (
	// Prompt is the task shown to the user. It is not the answer.
	Prompt  = "5 letters or digits in total. Only read the wavy characters with alternating light and dark shading; ignore pale patterns and small text."
	powBits = 14
)

// Clock and delay are replaceable in tests.
var (
	timeNow    = time.Now
	frameDelay = func() time.Duration {
		// Long enough to read a distorted glyph and which side it sits on.
		// A 1s gap only showed a flash and people could not keep up.
		n, err := rand.Int(rand.Reader, big.NewInt(2001))
		if err != nil {
			return 4 * time.Second
		}
		return time.Duration(3000+n.Int64()) * time.Millisecond
	}
)

type challenge struct {
	Hash      string `json:"h"`
	Mark      string `json:"m"`
	Trap      int    `json:"t"`
	Seed      int64  `json:"s"`
	Issued    int    `json:"i"`
	NotBefore int64  `json:"n"`
	Expires   int64  `json:"e"`
	Fails     int    `json:"f"`
	Bind      string `json:"b"`
	PowSalt   string `json:"p"`
}

// StartResult is a new challenge. Bind is the raw cookie value.
type StartResult struct {
	ID      string
	Bind    string
	Prompt  string
	PowSalt string
	PowBits int
}

// FrameResult is either a GIF or an instruction to wait.
type FrameResult struct {
	GIF   []byte
	Wait  time.Duration
	Done  bool
	Index int
}

// Start opens a challenge for ip. Bind must be echoed by later requests.
func Start(ctx context.Context, ip string) (StartResult, error) {
	return start(ctx, getStore(), ip)
}

func start(ctx context.Context, st Store, ip string) (StartResult, error) {
	n, err := st.Incr(ctx, "captcha:rate:"+ip, rateWindow)
	if err != nil {
		return StartResult{}, err
	}
	if n > startBudget {
		return StartResult{}, ErrRateLimited
	}
	seed, err := randomSeed()
	if err != nil {
		return StartResult{}, err
	}
	bind, err := randomHex(16)
	if err != nil {
		return StartResult{}, err
	}
	salt, err := randomHex(8)
	if err != nil {
		return StartResult{}, err
	}
	id := uuid.NewString()
	glyphs, trap := puzzleFromSeed(seed)
	ch := challenge{
		Hash:    hashCode(withoutTrap(glyphs, trap)),
		Mark:    hashCode(glyphs[trap : trap+1]),
		Trap:    trap,
		Seed:    seed,
		Expires: timeNow().Add(lifeTime).UnixMilli(),
		Bind:    hashBind(bind),
		PowSalt: salt,
	}
	raw, err := json.Marshal(ch)
	if err != nil {
		return StartResult{}, err
	}
	if err := st.Put(ctx, chKey(id), raw, lifeTime); err != nil {
		return StartResult{}, err
	}
	return StartResult{ID: id, Bind: bind, Prompt: Prompt, PowSalt: salt, PowBits: powBits}, nil
}

// Frame releases the next character, or says how long to wait.
func Frame(ctx context.Context, id, bind string) (FrameResult, error) {
	return frame(ctx, getStore(), id, bind)
}

func frame(ctx context.Context, st Store, id, bind string) (FrameResult, error) {
	var result FrameResult
	err := st.Update(ctx, chKey(id), lifeTime, func(prev []byte) ([]byte, error) {
		// Retried writes must not keep a frame from the attempt that did not commit.
		result = FrameResult{}
		ch, err := decode(prev, bind)
		if err != nil {
			return nil, err
		}
		now := timeNow()
		if ch.Issued >= Total {
			result = FrameResult{Done: true, Index: Total}
			return prev, nil
		}
		if now.Before(time.UnixMilli(ch.NotBefore)) {
			result = FrameResult{
				Wait:  time.UnixMilli(ch.NotBefore).Sub(now),
				Index: ch.Issued,
			}
			return prev, nil
		}
		glyphs, trap := puzzleFromSeed(ch.Seed)
		gifBytes, err := renderGIF(ch.Seed+int64(ch.Issued+1)*1000003, glyphs[ch.Issued], ch.Issued == trap)
		if err != nil {
			return nil, err
		}
		delay := frameDelay()
		ch.Issued++
		if ch.Issued < Total {
			ch.NotBefore = now.Add(delay).UnixMilli()
		}
		raw, err := json.Marshal(ch)
		if err != nil {
			return nil, err
		}
		result = FrameResult{GIF: gifBytes, Index: ch.Issued, Wait: delay, Done: ch.Issued >= Total}
		if result.Done {
			result.Wait = 0
		}
		return raw, nil
	})
	if errors.Is(err, ErrNotFound) {
		return FrameResult{}, ErrInvalid
	}
	return result, err
}

// Verify checks the answer and returns a one-time token.
// homepage is a honeypot: real clients leave it empty.
func Verify(ctx context.Context, id, bind, answer, homepage, nonce string) (string, error) {
	return verify(ctx, getStore(), id, bind, answer, homepage, nonce)
}

func verify(ctx context.Context, st Store, id, bind, answer, homepage, nonce string) (string, error) {
	var token string
	rejected := false
	err := st.Update(ctx, chKey(id), lifeTime, func(prev []byte) ([]byte, error) {
		token = ""
		rejected = false
		ch, err := decode(prev, bind)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(homepage) != "" {
			return nil, nil
		}
		if ch.Issued < Total {
			return nil, ErrEarly
		}
		if !powOK(ch.PowSalt, nonce, powBits) {
			return nil, ErrPow
		}
		got := normalize(answer)
		match := false
		if len(got) == Total && ch.Trap >= 0 && ch.Trap < Total {
			mark := hashCode(got[ch.Trap : ch.Trap+1])
			easy := hashCode(withoutTrap(got, ch.Trap))
			bot := subtle.ConstantTimeCompare([]byte(mark), []byte(ch.Mark)) == 1
			ok := subtle.ConstantTimeCompare([]byte(easy), []byte(ch.Hash)) == 1
			match = ok && !bot
		}
		if !match {
			rejected = true
			ch.Fails++
			if ch.Fails >= maxFails {
				return nil, nil
			}
			return json.Marshal(ch)
		}
		issued, err := randomHex(24)
		if err != nil {
			return nil, err
		}
		token = "local." + issued
		return nil, nil
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", ErrInvalid
		}
		return "", err
	}
	if rejected {
		return "", ErrWrong
	}
	if token == "" {
		return "", ErrInvalid
	}
	if err := st.Put(ctx, passKey(token), []byte("1"), tokenLife); err != nil {
		return "", err
	}
	return token, nil
}

// ConsumeToken burns a previously issued pass token.
func ConsumeToken(ctx context.Context, token string) (bool, error) {
	if !strings.HasPrefix(token, "local.") || len(token) < 16 {
		return false, nil
	}
	n, err := getStore().Del(ctx, passKey(token))
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func decode(prev []byte, bind string) (challenge, error) {
	if len(prev) == 0 {
		return challenge{}, ErrInvalid
	}
	var ch challenge
	if err := json.Unmarshal(prev, &ch); err != nil {
		return challenge{}, ErrInvalid
	}
	if timeNow().After(time.UnixMilli(ch.Expires)) {
		return challenge{}, ErrInvalid
	}
	if subtle.ConstantTimeCompare([]byte(ch.Bind), []byte(hashBind(bind))) != 1 {
		return challenge{}, ErrInvalid
	}
	return ch, nil
}

func puzzleFromSeed(seed int64) (glyphs string, trap int) {
	rng := mrand.New(mrand.NewPCG(uint64(seed), 0x9e3779b97f4a7c15))
	var buf [Total]byte
	for i := 0; i < Total; i++ {
		buf[i] = alphabet[rng.IntN(len(alphabet))]
	}
	return string(buf[:]), rng.IntN(Total)
}

func withoutTrap(glyphs string, trap int) string {
	return glyphs[:trap] + glyphs[trap+1:]
}

func powOK(salt, nonce string, bits int) bool {
	if salt == "" || len(nonce) == 0 || len(nonce) > 32 {
		return false
	}
	sum := sha256.Sum256([]byte(salt + ":" + nonce))
	return leadingZeroBits(sum[:]) >= bits
}

func leadingZeroBits(sum []byte) int {
	n := 0
	for _, b := range sum {
		if b == 0 {
			n += 8
			continue
		}
		for mask := byte(0x80); mask > 0; mask >>= 1 {
			if b&mask != 0 {
				return n
			}
			n++
		}
	}
	return n
}

func hashCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

func hashBind(bind string) string {
	sum := sha256.Sum256([]byte(bind))
	return hex.EncodeToString(sum[:])
}

func normalize(answer string) string {
	answer = strings.TrimSpace(answer)
	answer = strings.ReplaceAll(answer, " ", "")
	answer = strings.ReplaceAll(answer, "\u3000", "")
	var b strings.Builder
	b.Grow(len(answer))
	for _, r := range answer {
		switch {
		case r >= '０' && r <= '９':
			b.WriteByte(byte('0' + (r - '０')))
		case r >= 'Ａ' && r <= 'Ｚ':
			b.WriteByte(byte('A' + (r - 'Ａ')))
		case r >= 'ａ' && r <= 'ｚ':
			b.WriteByte(byte('A' + (r - 'ａ')))
		default:
			b.WriteRune(r)
		}
	}
	return strings.ToUpper(b.String())
}

func randomSeed() (int64, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return 0, err
	}
	n := int64(0)
	for _, b := range buf {
		n = n<<8 | int64(b)
	}
	if n < 0 {
		n = -n
	}
	return n, nil
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func chKey(id string) string    { return "captcha:ch:" + id }
func passKey(tok string) string { return "captcha:ok:" + tok }
