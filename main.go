package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/SirDataFR/iabtcfv2"
)

// Replace with Adserve's real ID from https://vendor-list.consensu.org/
// TCF vendor IDs are 16-bit (1–65535).
const adserveVendorID = 613

// strictMode enforces TCF v2.3: TC strings without disclosedVendors are invalid.
const strictMode = false

var jsBanner []byte

func main() {
	var err error
	jsBanner, err = os.ReadFile("ads_banner.js")
	if err != nil {
		log.Fatalf("failed to read ads_banner.js: %v", err)
	}

	http.HandleFunc("/ad", handleAd)
	http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	log.Printf("TCF v2.3 POC — vendorID: %d, strictMode: %v", adserveVendorID, strictMode)
	log.Fatal(http.ListenAndServe(":9090", nil))
}

type tcfPayload struct {
	GDPR            int    `json:"gdpr"`
	ConsentOK       bool   `json:"consent_ok"`
	VendorDisclosed string `json:"vendor_disclosed"` // "true" | "false" | "unknown"
	Decision        string `json:"decision"`
	Reason          string `json:"reason"`
}

func handleAd(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	gdpr := parseIntParam(q.Get("gdpr"))
	tcString := q.Get("gdpr_consent")
	siteID := q.Get("site_id")

	payload := evaluate(gdpr, tcString)

	log.Printf("site_id=%s gdpr=%d consent_ok=%v vendor_disclosed=%s decision=%s reason=%q",
		siteID, payload.GDPR, payload.ConsentOK, payload.VendorDisclosed, payload.Decision, payload.Reason)

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// JSON is valid JS — inject directly as an object literal, no escaping needed.
	js := strings.ReplaceAll(string(jsBanner), "{{tcf}}", string(jsonBytes))

	w.Header().Set("Content-Type", "text/javascript")
	fmt.Fprint(w, js)
}

// evaluate implements the TCF v2.3 GDPR decision logic.
// Consent is read from the TC string (VendorConsents), never from a URL flag.
//
//	gdpr != 1                              → allow  (non-GDPR traffic)
//	TC string invalid/missing              → block  (can't verify consent)
//	vendor_disclosed = false               → block  (not shown in CMP)
//	vendor_disclosed = unknown + strict    → block  (missing segment = invalid)
//	vendor consent not granted             → block  (user did not consent)
//	vendor consent granted                 → allow
func evaluate(gdpr int, tcString string) tcfPayload {
	if gdpr != 1 {
		return tcfPayload{gdpr, false, "unknown", "allow", "non-GDPR traffic"}
	}

	if tcString == "" {
		return tcfPayload{gdpr, false, "unknown", "block", "no TC string provided"}
	}

	// Decode once, use for all checks.
	tc, err := iabtcfv2.Decode(tcString)
	if err != nil {
		return tcfPayload{gdpr, false, "unknown", "block", "invalid TC string"}
	}

	// 1. Disclosure check (TCF v2.3)
	vendorDisclosed := "unknown"
	if tc.DisclosedVendors != nil {
		if tc.DisclosedVendors.IsVendorDisclosed(adserveVendorID) {
			vendorDisclosed = "true"
		} else {
			return tcfPayload{gdpr, false, "false", "block", "vendor not disclosed in CMP UI"}
		}
	} else if strictMode {
		return tcfPayload{gdpr, false, "unknown", "block", "disclosedVendors segment missing (strict mode)"}
	}

	// 2. Vendor consent check — read from TC string, not from a URL flag.
	consentOK := tc.IsVendorAllowed(adserveVendorID)
	if !consentOK {
		return tcfPayload{gdpr, false, vendorDisclosed, "block", "vendor consent not granted"}
	}

	return tcfPayload{gdpr, true, vendorDisclosed, "allow", "vendor consent granted"}
}

func parseIntParam(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}
