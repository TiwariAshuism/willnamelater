package model

import "github.com/getnyx/influaudit/backend/internal/platform/errs"

// Niche is an influencer's content category. It keys scoring weights and
// benchmark tables downstream, so its values are stable and lowercase.
type Niche string

// The Niche values below are the supported content categories. The set is
// closed: config loading and the scoring benchmarks are keyed on exactly these
// values, so a handle cannot be filed under a category the rest of the system
// has no weights for.
const (
	// NicheBeauty covers cosmetics, skincare, and grooming content.
	NicheBeauty Niche = "beauty"
	// NicheFashion covers apparel and style content.
	NicheFashion Niche = "fashion"
	// NicheFitness covers training, workouts, and physical performance.
	NicheFitness Niche = "fitness"
	// NicheFood covers cooking, recipes, and dining.
	NicheFood Niche = "food"
	// NicheTravel covers destinations and travel experiences.
	NicheTravel Niche = "travel"
	// NicheTechnology covers gadgets, software, and consumer electronics.
	NicheTechnology Niche = "technology"
	// NicheGaming covers video games and streaming.
	NicheGaming Niche = "gaming"
	// NicheLifestyle covers day-in-the-life and general lifestyle content.
	NicheLifestyle Niche = "lifestyle"
	// NicheBusiness covers entrepreneurship and professional content.
	NicheBusiness Niche = "business"
	// NicheEducation covers teaching and explanatory content.
	NicheEducation Niche = "education"
	// NicheEntertainment covers comedy, film, and general entertainment.
	NicheEntertainment Niche = "entertainment"
	// NicheHealth covers wellness, nutrition, and mental health.
	NicheHealth Niche = "health"
	// NicheParenting covers family and childcare content.
	NicheParenting Niche = "parenting"
	// NicheFinance covers personal finance and investing.
	NicheFinance Niche = "finance"
	// NicheOther is the catch-all for content outside the named categories.
	NicheOther Niche = "other"
)

// Valid reports whether n is one of the defined categories.
func (n Niche) Valid() bool {
	switch n {
	case NicheBeauty, NicheFashion, NicheFitness, NicheFood, NicheTravel,
		NicheTechnology, NicheGaming, NicheLifestyle, NicheBusiness,
		NicheEducation, NicheEntertainment, NicheHealth, NicheParenting,
		NicheFinance, NicheOther:
		return true
	default:
		return false
	}
}

// ParseNiche validates raw and returns the corresponding Niche. An empty or
// unrecognized value yields errs.KindInvalid with a stable code, never the raw
// input echoed back into an error the client cannot act on.
func ParseNiche(raw string) (Niche, error) {
	n := Niche(raw)
	if !n.Valid() {
		return "", errs.New(errs.KindInvalid, "influencer.niche_invalid", "niche is not a recognized category")
	}
	return n, nil
}
