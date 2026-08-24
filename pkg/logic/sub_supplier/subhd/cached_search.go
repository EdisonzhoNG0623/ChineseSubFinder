package subhd

import (
	"errors"
	"strings"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/go-rod/rod"
)

var errSubHDSearchBlocked = errors.New("subhd search page blocked by site verification")

func (s *Supplier) ensureSearchCache() *subHDSearchCache {
	s.cacheInitOnce.Do(func() {
		s.searchCache = newSubHDSearchCache()
	})
	return s.searchCache
}

func (s *Supplier) cachedStep0(browser *rod.Browser, query subHDSearchQuery) (string, error) {
	cacheKey := settings.Get().AdvancedSettings.SuppliersSettings.SubHD.RootUrl + "|" + query.Keyword
	if detailURL, ok := s.ensureSearchCache().getDetail(cacheKey); ok {
		if detailURL == "" {
			s.log.Debugf("subhd search cache hit kind=negative query_kind=%s keyword=%q", query.Kind, query.Keyword)
		} else {
			s.log.Debugf("subhd search cache hit kind=positive query_kind=%s keyword=%q", query.Kind, query.Keyword)
		}
		return detailURL, nil
	}

	detailURL, cacheable, err := s.step0WithCacheability(browser, query.Keyword)
	if err != nil {
		return "", err
	}
	if cacheable {
		s.ensureSearchCache().putDetail(cacheKey, detailURL)
	} else {
		s.log.Debugf("subhd search result not cached query_kind=%s keyword=%q", query.Kind, query.Keyword)
	}
	return detailURL, nil
}

func (s *Supplier) cachedStep1(browser *rod.Browser, detailPageURL string) ([]HdListItem, error) {
	cacheKey := settings.Get().AdvancedSettings.SuppliersSettings.SubHD.RootUrl + "|" + detailPageURL
	if items, ok := s.ensureSearchCache().getList(cacheKey); ok {
		s.log.Debugf("subhd detail cache hit url=%q candidates=%d", detailPageURL, len(items))
		return items, nil
	}

	items, err := s.step1(browser, detailPageURL, false)
	if err != nil {
		return nil, err
	}
	s.ensureSearchCache().putList(cacheKey, items)
	return items, nil
}

func subHDSearchPageBlocked(body string) bool {
	normalized := strings.ToLower(body)
	for _, marker := range []string{
		"访问过于频繁", "请求过于频繁", "请完成安全验证", "安全验证中", "verify you are human",
		"cf-chl-", "challenge-platform", "checking your browser",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func subHDSearchPageHasNoResults(body string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(body), " "))
	for _, marker := range []string{"共 0 条", "共0条", "暂无相关字幕", "没有找到相关字幕"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
