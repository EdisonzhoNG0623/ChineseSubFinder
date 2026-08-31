package sub_supplier

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"
	"github.com/sirupsen/logrus"
)

type cooldownTestSupplier struct {
	checks int32
	log    *logrus.Logger
}

func (s *cooldownTestSupplier) CheckAlive() (bool, int64) {
	atomic.AddInt32(&s.checks, 1)
	return true, 1
}
func (s *cooldownTestSupplier) IsAlive() bool                { return true }
func (s *cooldownTestSupplier) GetSupplierName() string      { return "cooldown-test" }
func (s *cooldownTestSupplier) OverDailyDownloadLimit() bool { return false }
func (s *cooldownTestSupplier) GetLogger() *logrus.Logger    { return s.log }
func (s *cooldownTestSupplier) GetSubListFromFile4Movie(string) ([]supplier.SubInfo, error) {
	return nil, nil
}
func (s *cooldownTestSupplier) GetSubListFromFile4Series(*series.SeriesInfo) ([]supplier.SubInfo, error) {
	return nil, nil
}
func (s *cooldownTestSupplier) GetSubListFromFile4Anime(*series.SeriesInfo) ([]supplier.SubInfo, error) {
	return nil, nil
}

func TestCheckSubSiteStatusRemovesSupplierDuringCooldown(t *testing.T) {
	previous := processSupplierHealthCooldown
	processSupplierHealthCooldown = newSupplierHealthCooldown()
	defer func() { processSupplierHealthCooldown = previous }()

	now := time.Now()
	for i := 0; i < supplierFailureThreshold; i++ {
		processSupplierHealthCooldown.record("cooldown-test", false, now)
	}

	fake := &cooldownTestSupplier{log: logrus.New()}
	hub := NewSubSupplierHub(fake)
	status := hub.CheckSubSiteStatus()
	if atomic.LoadInt32(&fake.checks) != 0 {
		t.Fatal("supplier health endpoint was called during cooldown")
	}
	if len(hub.Suppliers) != 0 {
		t.Fatalf("cooling supplier remained active: %d suppliers", len(hub.Suppliers))
	}
	if len(status.SubSiteStatus) != 1 || status.SubSiteStatus[0].Valid {
		t.Fatalf("unexpected cooldown status: %+v", status.SubSiteStatus)
	}
}
