package crawler

import (
	"github.com/cyberpsych0s1s/quert/internal/config"
	"github.com/cyberpsych0s1s/quert/internal/extractor"
)

// ExtractorConfigFromContent builds an extractor configuration from the user's
// content configuration. It starts from the extractor defaults so fields the
// content config does not cover (content/boilerplate selectors, extract toggles)
// keep sensible values, then overlays the content settings.
//
// This closes the gap where NewCrawlerEngine always used the hardcoded extractor
// defaults, so config.yaml's content.* settings (quality threshold, text length
// limits, boilerplate/main-content toggles) had no effect at runtime.
//
// cc is expected to come from config.LoadConfig (which applies defaults). A nil
// cc returns the plain extractor defaults.
func ExtractorConfigFromContent(cc *config.ContentConfig) *extractor.ExtractorConfig {
	ec := extractor.GetDefaultExtractorConfig()
	if cc == nil {
		return ec
	}

	if cc.MinTextLength > 0 {
		ec.MinTextLength = cc.MinTextLength
	}
	if cc.MaxTextLength > 0 {
		ec.MaxTextLength = cc.MaxTextLength
	}
	if cc.QualityThreshold > 0 {
		ec.QualityThreshold = cc.QualityThreshold
	}
	ec.RemoveBoilerplate = cc.RemoveBoilerplate
	ec.ExtractMainContent = cc.ExtractMainContent
	ec.PreserveFormatting = cc.PreserveFormatting
	ec.NormalizeWhitespace = cc.NormalizeWhitespace

	return ec
}
