package domain_test

import (
	"math"
	"testing"

	"wf/backend/internal/geo/internal/domain"
)

func TestCoverRadiusMeters(t *testing.T) {
	cases := []struct {
		population int64
		want       float64
	}{
		{-5, 2000},                    // мусор — как пустое место
		{0, 2000},                     // нижняя граница
		{500, 2447.213595499958},      // село на 500 жителей — ≈ 2,4 км (спека §4.3)
		{10_000, 4000},                // 2000 + 20·100
		{59_198, 6866.127824050659},   // Михайловск
		{433_931, 15174.687852089703}, // Ставрополь
		{1_960_000, 30000},            // ровно потолок: 2000 + 20·1400
		{1_960_001, 30000},            // выше потолка — 30 км
		{12_000_000, 30000},           // Москва
		{math.MaxInt64, 30000},        // без переполнения
	}
	for _, c := range cases {
		// !(… <= …), а не … > …: NaN не проходит ни одно сравнение и проскочил бы
		if got := domain.CoverRadiusMeters(c.population); !(math.Abs(got-c.want) <= 1e-6) {
			t.Errorf("CoverRadiusMeters(%d) = %v, ждали %v", c.population, got, c.want)
		}
	}
}

func TestHereSearchRadius(t *testing.T) {
	if domain.HereSearchRadiusMeters != 30000 {
		t.Fatalf("радиус поиска %d, ждали 30000 (спека §4.3)", domain.HereSearchRadiusMeters)
	}
}

func TestPlaceAllowed(t *testing.T) {
	cases := []struct {
		class, code string
		population  int64
		want        bool
	}{
		{"P", "PPL", 500, true},         // ровно порог
		{"P", "PPL", 499, false},        // ниже порога
		{"P", "PPLA", 0, true},          // центр субъекта — при любом населении
		{"P", "PPLA2", 10, true},        // центр района
		{"P", "PPLA3", 10, true},        // центр третьего уровня
		{"P", "PPLA4", 10, true},        // центр четвёртого уровня
		{"P", "PPLC", 0, true},          // столица
		{"P", "PPLX", 1_000_000, false}, // часть города: район не «накрывает» Москву
		{"P", "PPLQ", 5000, false},      // заброшенный
		{"P", "PPLH", 5000, false},      // исторический
		{"P", "PPLW", 5000, false},      // разрушенный
		{"P", "PPLCH", 5000, false},     // бывшая столица
		{"A", "ADM1", 3_000_000, false}, // не населённый пункт
		{"", "PPL", 5000, false},        // пустой класс
	}
	for _, c := range cases {
		if got := domain.PlaceAllowed(c.class, c.code, c.population); got != c.want {
			t.Errorf("PlaceAllowed(%q, %q, %d) = %v, ждали %v", c.class, c.code, c.population, got, c.want)
		}
	}
}
