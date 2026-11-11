// Package seed generates deterministic synthetic customers and their
// shopping activity. Everything is fake; emails use reserved example domains.
//
// Customers are generated from behavioural archetypes (VIPs, cart abandoners,
// lapsed buyers, ...) so that order and cart histories are coherent rather
// than independent random events.
package seed

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"time"
)

// rngSeed fixes the random stream: the same (n, now) always yields the same data.
const rngSeed = 20260101

const day = 24 * time.Hour

type Customer struct {
	ID           int64
	FirstName    string
	LastName     string
	Email        string
	Phone        *string
	City         string
	Language     string
	EmailConsent bool
	SMSConsent   bool
	CreatedAt    time.Time
	LastActiveAt time.Time
}

type Order struct {
	CustomerID int64
	Category   string
	TotalCents int64
	PlacedAt   time.Time
}

type CartAddition struct {
	CustomerID int64
	Category   string
	AddedAt    time.Time
}

type Dataset struct {
	Customers     []Customer
	Orders        []Order
	CartAdditions []CartAddition
}

type weighted[T any] struct {
	v T
	w float64
}

func pick[T any](r *rand.Rand, items []weighted[T]) T {
	var total float64
	for _, it := range items {
		total += it.w
	}
	x := r.Float64() * total
	for _, it := range items {
		if x < it.w {
			return it.v
		}
		x -= it.w
	}
	return items[len(items)-1].v
}

// Cities are ASCII so that rule values like "Istanbul" match exactly.
var cities = []weighted[string]{
	{"Istanbul", 27}, {"Ankara", 11}, {"Izmir", 9}, {"Bursa", 5}, {"Antalya", 5},
	{"Adana", 4}, {"Konya", 3.5}, {"Gaziantep", 3.5}, {"Mersin", 3}, {"Kocaeli", 3},
	{"Kayseri", 2.5}, {"Eskisehir", 2.5}, {"Diyarbakir", 2.5}, {"Samsun", 2},
	{"Denizli", 2}, {"Trabzon", 2}, {"Sakarya", 2}, {"Mugla", 1.5},
	{"Balikesir", 1.5}, {"Malatya", 1.5},
}

// Cosmopolitan cities have more English-speaking customers.
var cosmopolitan = map[string]bool{"Istanbul": true, "Izmir": true, "Antalya": true, "Mugla": true}

// Language mix: ~3% German (deliberately not a supported content language,
// so the delivery fallback is exercised), English 12% (28% in cosmopolitan
// cities), Turkish otherwise (~75% overall). p is uniform in [0, 100).
const (
	germanShare       = 3
	englishShare      = 12
	englishShareCosmo = 28
)

type namePool struct{ female, male, surnames []string }

var namesByLanguage = map[string]namePool{
	"tr": {femaleNames, maleNames, surnames},
	"en": {englishFemale, englishMale, englishSurnames},
	"de": {germanFemale, germanMale, germanSurnames},
}

func languageFor(p float64, city string) string {
	en := float64(englishShare)
	if cosmopolitan[city] {
		en = englishShareCosmo
	}
	switch {
	case p < germanShare:
		return "de"
	case p < germanShare+en:
		return "en"
	}
	return "tr"
}

type category struct {
	name   string
	median float64 // median order value, TRY
}

var categories = []weighted[category]{
	{category{"fashion", 650}, 26}, {category{"electronics", 1800}, 18},
	{category{"home", 700}, 16}, {category{"beauty", 300}, 14},
	{category{"grocery", 250}, 10}, {category{"sports", 600}, 8},
	{category{"books", 150}, 5}, {category{"toys", 350}, 3},
}

var (
	femaleNames = []string{"Ayşe", "Fatma", "Emine", "Hatice", "Zeynep", "Elif", "Merve", "Büşra", "Esra", "Selin", "Deniz", "Ece", "Defne", "Yasemin", "Gamze", "Burcu", "Seda", "Aylin", "Derya", "Pınar", "Sevgi", "Nur", "İrem", "Melis", "Cansu"}
	maleNames   = []string{"Mehmet", "Mustafa", "Ahmet", "Ali", "Hüseyin", "Hasan", "İbrahim", "Murat", "Emre", "Burak", "Can", "Cem", "Oğuz", "Kerem", "Onur", "Serkan", "Volkan", "Barış", "Tolga", "Yusuf", "Eren", "Kaan", "Mert", "Ozan", "Umut"}
	surnames    = []string{"Yılmaz", "Kaya", "Demir", "Şahin", "Çelik", "Yıldız", "Yıldırım", "Öztürk", "Aydın", "Özdemir", "Arslan", "Doğan", "Kılıç", "Aslan", "Çetin", "Kara", "Koç", "Kurt", "Özkan", "Şimşek", "Polat", "Korkmaz", "Çakır", "Erdoğan", "Güneş", "Aksoy", "Avcı", "Türkmen", "Bozkurt", "Tekin", "Acar", "Karaca", "Uçar", "Taş", "Sönmez"}

	// Reserved by RFC 2606: mail to these domains never reaches a real person.
	englishFemale   = []string{"Emma", "Olivia", "Sophie", "Charlotte", "Emily", "Grace", "Hannah", "Lucy", "Amelia", "Chloe", "Jessica", "Laura", "Sarah", "Rachel", "Megan"}
	englishMale     = []string{"James", "Oliver", "William", "Jack", "Thomas", "Daniel", "Matthew", "Henry", "George", "Samuel", "David", "Michael", "Ryan", "Adam", "Luke"}
	englishSurnames = []string{"Smith", "Johnson", "Williams", "Brown", "Taylor", "Wilson", "Davies", "Evans", "Thomas", "Roberts", "Walker", "Wright", "Thompson", "White", "Hughes", "Green", "Hall", "Clarke", "Baker", "Turner"}
	germanFemale    = []string{"Anna", "Lena", "Laura", "Julia", "Sophie", "Lea", "Katharina", "Sarah", "Hannah", "Marie"}
	germanMale      = []string{"Lukas", "Jonas", "Felix", "Maximilian", "Leon", "Paul", "Tobias", "Florian", "Niklas", "Jan"}
	germanSurnames  = []string{"Müller", "Schmidt", "Schneider", "Fischer", "Weber", "Meyer", "Wagner", "Becker", "Schulz", "Hoffmann", "Koch", "Richter"}

	emailDomains = []weighted[string]{{"example.com", 60}, {"example.org", 25}, {"example.net", 15}}

	phonePrefixes = []string{"505", "532", "533", "535", "536", "538", "541", "542", "543", "544", "545", "551", "552", "553", "554"}

	ascii = strings.NewReplacer(
		"ç", "c", "Ç", "C", "ğ", "g", "Ğ", "G", "ı", "i", "İ", "I",
		"ö", "o", "Ö", "O", "ş", "s", "Ş", "S", "ü", "u", "Ü", "U",
	)

	pEmailConsent  = 0.78
	pSMSConsent    = 0.55
	pHasPhone      = 0.94
	pCartBeforeBuy = 0.35 // share of orders preceded by a cart addition
)

// span is an inclusive-exclusive range of days.
type span [2]float64

// archetypes describe how a customer behaves relative to "now". The ranges
// are chosen to line up with the audience definitions in internal/customers
// (e.g. win-back = last order > 90 days ago, VIP = 4+ orders and >= 5000 TRY).
var archetypes = []weighted[string]{
	{"vip", 5},
	{"loyal", 17},
	{"recent", 14},
	{"lapsing", 10},
	{"winback", 16},
	{"abandoner", 14},
	{"new", 14},
	{"dormant", 10},
}

// vipMinSpendCents makes VIP archetype customers genuinely high-value.
const vipMinSpendCents = 520_000

type history struct {
	created    time.Time
	lastActive time.Time
	orders     []Order
	carts      []CartAddition
}

type generator struct {
	r   *rand.Rand
	now time.Time
}

// Generate builds n customers with orders and cart events relative to now.
func Generate(n int, now time.Time) Dataset {
	g := &generator{r: rand.New(rand.NewPCG(rngSeed, rngSeed)), now: now.UTC().Truncate(time.Second)}

	var ds Dataset
	emails := make(map[string]bool, n)
	phones := make(map[string]bool, n)

	for i := 1; i <= n; i++ {
		id := int64(i)
		fav := pick(g.r, categories)
		h := g.history(pick(g.r, archetypes), fav)

		ds.Customers = append(ds.Customers, g.customer(id, h, emails, phones))
		for _, o := range h.orders {
			o.CustomerID = id
			ds.Orders = append(ds.Orders, o)
		}
		for _, c := range h.carts {
			c.CustomerID = id
			ds.CartAdditions = append(ds.CartAdditions, c)
		}
	}
	return ds
}

func (g *generator) history(archetype string, fav category) history {
	switch archetype {
	case "vip":
		h := g.buyer(fav, 4+g.r.IntN(11), span{1, 45}, span{150, 700}, 1.6, vipMinSpendCents)
		return g.finish(h, 14)
	case "loyal":
		return g.finish(g.buyer(fav, 2+g.r.IntN(5), span{5, 120}, span{60, 600}, 1, 0), 14)
	case "recent":
		return g.finish(g.buyer(fav, 1+g.r.IntN(2), span{0.2, 30}, span{0, 300}, 1, 0), 7)
	case "lapsing":
		return g.finish(g.buyer(fav, 1+g.r.IntN(3), span{31, 89}, span{0, 400}, 1, 0), 14)
	case "winback":
		return g.finish(g.buyer(fav, 1+g.r.IntN(6), span{95, 600}, span{0, 500}, 1, 0), 45)
	case "abandoner":
		// Added to cart in the last 30 days and did not buy afterwards.
		// Half have bought before (long ago), half never have.
		var h history
		if g.r.Float64() < 0.5 {
			h = g.buyer(fav, 1+g.r.IntN(4), span{40, 400}, span{0, 500}, 1, 0)
		} else {
			h.created = g.ago(35, 600)
		}
		for k := 1 + g.r.IntN(3); k > 0; k-- {
			h.carts = append(h.carts, CartAddition{Category: g.categoryFor(fav).name, AddedAt: g.ago(0.2, 29.5)})
		}
		return g.finish(h, 3)
	case "new":
		// Signed up within the last 30 days, no purchase yet.
		h := history{created: g.ago(0.1, 29.5)}
		if g.r.Float64() < 0.3 {
			for k := 1 + g.r.IntN(2); k > 0; k-- {
				h.carts = append(h.carts, CartAddition{Category: g.categoryFor(fav).name, AddedAt: g.between(h.created, g.now)})
			}
		}
		return g.finish(h, 5)
	default: // dormant: registered, browsed, never bought, gone quiet
		h := history{lastActive: g.ago(61, 400)}
		h.created = h.lastActive.Add(-g.days(5, 300))
		if g.r.Float64() < 0.5 {
			for k := 1 + g.r.IntN(3); k > 0; k-- {
				h.carts = append(h.carts, CartAddition{Category: g.categoryFor(fav).name, AddedAt: g.between(h.created, h.lastActive)})
			}
		}
		return h
	}
}

// buyer builds n orders whose last one is lastAgo days old and whose first one
// is spread days earlier; the customer registered shortly before the first.
// Some orders are preceded by a cart addition. mult scales order values and
// minSpend (cents, 0 = none) adds orders until the lifetime value is reached.
func (g *generator) buyer(fav category, n int, lastAgo, spread span, mult float64, minSpend int64) history {
	last := g.ago(lastAgo[0], lastAgo[1])
	first := last
	if n > 1 {
		first = last.Add(-g.days(spread[0], spread[1]))
	}
	h := history{created: first.Add(-g.days(1, 120)).Truncate(time.Second)}

	times := []time.Time{last}
	if n > 1 {
		times = append(times, first)
	}
	for len(times) < n {
		times = append(times, g.between(first, last))
	}

	var spent int64
	add := func(at time.Time) {
		cat := g.categoryFor(fav)
		o := Order{Category: cat.name, TotalCents: orderTotal(g.r, cat, mult), PlacedAt: at}
		h.orders = append(h.orders, o)
		spent += o.TotalCents
		if g.r.Float64() < pCartBeforeBuy {
			added := at.Add(-10*time.Minute - time.Duration(g.r.Int64N(int64(72*time.Hour)))).Truncate(time.Second)
			if !added.Before(h.created) {
				h.carts = append(h.carts, CartAddition{Category: cat.name, AddedAt: added})
			}
		}
	}
	for _, at := range times {
		add(at)
	}
	for spent < minSpend {
		add(g.between(first, last))
	}
	return h
}

// finish sets last_active_at to just after the latest known event (a customer
// may visit without buying), never in the future.
func (g *generator) finish(h history, maxLagDays float64) history {
	latest := h.created
	for _, o := range h.orders {
		if o.PlacedAt.After(latest) {
			latest = o.PlacedAt
		}
	}
	for _, c := range h.carts {
		if c.AddedAt.After(latest) {
			latest = c.AddedAt
		}
	}
	h.lastActive = latest.Add(g.days(0, maxLagDays)).Truncate(time.Second)
	if h.lastActive.After(g.now) {
		h.lastActive = g.now
	}
	return h
}

func (g *generator) customer(id int64, h history, emails, phones map[string]bool) Customer {
	r := g.r
	c := Customer{
		ID:           id,
		City:         pick(r, cities),
		CreatedAt:    h.created,
		LastActiveAt: h.lastActive,
	}
	c.Language = languageFor(r.Float64()*100, c.City)
	// Names fit the language: an English speaker named Hüseyin is possible
	// but rare (about 1 in 1000 foreign-language customers).
	names := namesByLanguage[c.Language]
	if c.Language != "tr" && r.Float64() < 0.001 {
		names = namesByLanguage["tr"]
	}
	first := pickString(r, names.female, names.male)
	last := names.surnames[r.IntN(len(names.surnames))]
	c.FirstName, c.LastName = first, last

	f, l := strings.ToLower(ascii.Replace(first)), strings.ToLower(ascii.Replace(last))
	local := [...]string{f + "." + l, f + l, f + "_" + l, f[:1] + "." + l}[r.IntN(4)]
	if r.Float64() < 0.35 {
		local += fmt.Sprint(r.IntN(100))
	}
	domain := "@" + pick(r, emailDomains)
	if emails[local+domain] {
		local += fmt.Sprint(id) // guarantees uniqueness
	}
	c.Email = local + domain
	emails[c.Email] = true

	if r.Float64() < pHasPhone {
		for {
			p := fmt.Sprintf("+90%s%07d", phonePrefixes[r.IntN(len(phonePrefixes))], r.IntN(10_000_000))
			if !phones[p] {
				phones[p] = true
				c.Phone = &p
				break
			}
		}
	}

	// Channels are independent; SMS additionally requires a phone number.
	c.EmailConsent = r.Float64() < pEmailConsent
	c.SMSConsent = r.Float64() < pSMSConsent && c.Phone != nil
	return c
}

// categoryFor favours the customer's preferred category.
func (g *generator) categoryFor(fav category) category {
	if g.r.Float64() < 0.65 {
		return fav
	}
	return pick(g.r, categories)
}

// days returns a random duration between lo and hi days.
func (g *generator) days(lo, hi float64) time.Duration {
	return time.Duration((lo + g.r.Float64()*(hi-lo)) * float64(day))
}

// ago returns a time lo..hi days before now.
func (g *generator) ago(lo, hi float64) time.Time {
	return g.now.Add(-g.days(lo, hi)).Truncate(time.Second)
}

func (g *generator) between(from, to time.Time) time.Time {
	return from.Add(time.Duration(g.r.Float64() * float64(to.Sub(from)))).Truncate(time.Second)
}

func pickString(r *rand.Rand, a, b []string) string {
	if r.IntN(2) == 0 {
		return a[r.IntN(len(a))]
	}
	return b[r.IntN(len(b))]
}

// orderTotal is log-normal around the category median, in kuruş.
func orderTotal(r *rand.Rand, c category, mult float64) int64 {
	v := c.median * mult * math.Exp(0.6*r.NormFloat64())
	v = min(max(v, 20), 60000)
	return int64(math.Round(v * 100))
}
