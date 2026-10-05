package domain

import "math"

// HereSearchRadiusMeters — радиус поиска кандидатов «here» и потолок радиуса накрытия (спека §4.3).
const HereSearchRadiusMeters = 30000

const (
	minCoverMeters      = 2000 // радиус накрытия самого маленького места
	coverMetersPerSqrt  = 20   // прибавка на √population
	minPlacePopulation  = 500  // порог отбора мест импорта (§5.4 шаг 3)
	placeClassPopulated = "P"  // класс GeoNames «населённый пункт»
)

// CoverRadiusMeters — r(pop) = clamp(2 км + 20 м · √population, 2 км, 30 км): место «накрывает»
// точку, если расстояние до неё не больше r (спека §4.3). Москва (√12 млн ≈ 3,5 тыс.) — 30 км,
// село на 500 жителей — ≈ 2,4 км. Отрицательное население — как 0.
func CoverRadiusMeters(population int64) float64 {
	population = max(population, 0)
	r := minCoverMeters + coverMetersPerSqrt*math.Sqrt(float64(population))
	return min(r, HereSearchRadiusMeters)
}

// excludedCodes — части и следы населённых пунктов: PPLX — часть города (район «накрыл» бы
// город), PPLQ — заброшенный, PPLH — исторический, PPLW — разрушенный, PPLCH — бывшая столица.
var excludedCodes = map[string]bool{"PPLX": true, "PPLQ": true, "PPLH": true, "PPLW": true, "PPLCH": true}

// adminCenters — административные центры: берутся при любом населении.
var adminCenters = map[string]bool{"PPLA": true, "PPLA2": true, "PPLA3": true, "PPLA4": true, "PPLC": true}

// PlaceAllowed — отбор мест импорта (спека §5.4 шаг 3): класс P, код не из excludedCodes,
// население ≥ 500 или административный центр.
func PlaceAllowed(featureClass, featureCode string, population int64) bool {
	if featureClass != placeClassPopulated || excludedCodes[featureCode] {
		return false
	}
	return population >= minPlacePopulation || adminCenters[featureCode]
}
