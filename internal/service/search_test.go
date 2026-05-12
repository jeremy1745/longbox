package service

import (
	"testing"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/newznab"
)

// TestIsComicRelease_RejectsTVEpisodeMarkers verifies that release titles
// carrying SxxExx / Sxxxxx / NxX episode notation are rejected. These tokens
// are the signature of TV releases and never appear in comic releases.
//
// Origin: live grabs that polluted the user's library, all of which contained
// these markers but were not stopped by the (extension-only) filter.
func TestIsComicRelease_RejectsTVEpisodeMarkers(t *testing.T) {
	cases := []string{
		"Wonder.Man.S01.2026.1080p.DSNP.WEB-DL.DDP5.1.Atmos.H.264-HDSWEB",
		"Beast.Wars.Transformers.S02E05.Maximal.No.More.DVDRip.XviD.iNT-WPi",
		"Family.Guy.S24E12.Lower.G.i.Joe.1080p.HEVC.x265-MeGusta",
		"Luxury.Escapes.S2025E03.WEB.H264-RBB",
		"One.Piece.S21E23.Finally.Clashing.1080p.H.265.English.Dub-G",
		"La Dimension Desconocida [1959] 1x11 Y Cuando El Cielo Se Abrio [The Twilight Zone]",
	}
	for _, title := range cases {
		if isComicRelease(title) {
			t.Errorf("expected TV release to be rejected, got accepted: %q", title)
		}
	}
}

// TestIsComicRelease_RejectsVideoQualityMarkers verifies that resolution,
// container, and codec tags hard-reject the release. Scene/p2p video naming
// is unambiguous on these.
func TestIsComicRelease_RejectsVideoQualityMarkers(t *testing.T) {
	cases := []string{
		"Captain.America.Brave.New.World.2025.1080p.DSNP.WEB-DL.DDPA.5.1.H.264-PiRaTeS",
		"Deadpool.and.Wolverine.2024.1080p.DSNP.WEB-DL.DDP5.1.Atmos.H.264-TURG",
		"Ozzy.No.Escape.From.Now.2025.720p.AMZN.WEB-DL.DDP5.1.H.264-FLUX",
		"Hellboy [2019] MULTi VFF 2160p 10bit 4KLight HDR BluRay x265 AAC 7.1-QTZ",
		"Dieced.Reloaded.2025.1080p.AMZN.WEB-DL.DD.5.1.H.264-playWEB",
		"The.Ibiza.Final.Boss.Haircuts.and.Hangovers.2025.720p.HDTV.H264-JFF",
	}
	for _, title := range cases {
		if isComicRelease(title) {
			t.Errorf("expected video release to be rejected, got accepted: %q", title)
		}
	}
}

// TestIsComicRelease_RejectsAdultContent verifies that scene-style adult
// release tokens hard-reject the release. These tokens are unambiguous
// signals — they do not appear in comic release titles.
func TestIsComicRelease_RejectsAdultContent(t *testing.T) {
	cases := []string{
		"LoveHerBoobs-Karina.King-Quick.Escape.[30.09.2025]-XXX",
		"ManyVids.2025.Penny.Barber.Best.Sleepover.EVER.XXX.720p-XLeech",
		"LatinaCasting.2024.Matea.Pemon.Is.Such.A.Newbie.To.The.Industy.XXX.1080p.HEVC.x265.PRT",
		"EnjoyX-Mary.Rock-Dirty.Story.Edition.Mary.Rock.&.Rocket.Powers.[14.11.2025]-XXX",
		"UltimateSurrender.13.07.23.DragonLily.Penny.Barber.Syd.Blakovich.XXX.720p.x264-PAYiSO",
	}
	for _, title := range cases {
		if isComicRelease(title) {
			t.Errorf("expected adult release to be rejected, got accepted: %q", title)
		}
	}
}

// TestIsComicRelease_AcceptsLegitComics verifies the filter does NOT
// regress on real comic releases. Comic release titles use a variety of
// scene-style tags (Empire, Webrip, Digital, GLOBAL, etc.) that must
// continue to pass.
func TestIsComicRelease_AcceptsLegitComics(t *testing.T) {
	cases := []string{
		"Transformers 008 (2025) (Digital) (Empire)",
		"Absolute Batman 006 (2025) (Webrip) (The Last Kryptonian-DCP)",
		"Detective Comics 2021 Annual (2022) (Webrip)",
		"Wolverine 011 (2024).cbz",
		"Batman - The Knight 010 (of 10) (2022) (Webrip)",
		"Alice.Never.After.001.(2022).(Empire)",
		"Supergirl - Woman of Tomorrow 005 (of 08) (2022) (Webrip)",
		"A Vicious Circle 002 (of 03) (2023) (Digital-Empire)",
		"BRZRKR 012 (2024) (Webrip) (The Last Kryptonian-DCP)",
		"Saga 064 (2024) (Digital) (Zone-Empire)",
	}
	for _, title := range cases {
		if !isComicRelease(title) {
			t.Errorf("expected comic release to be accepted, got rejected: %q", title)
		}
	}
}

// TestScoreResult_WordBoundaryOnSeriesName verifies that the series-name
// substring check requires word boundaries. Otherwise short series titles
// match as substrings inside unrelated words (e.g., series "Star" matching
// release "Pornstar.XXX...", or series "Lust" matching "Wanderlust").
func TestScoreResult_WordBoundaryOnSeriesName(t *testing.T) {
	yearPtr := func(y int) *int { return &y }
	series := &model.Series{Title: "Star", Year: yearPtr(2024)}
	issue := &model.Issue{IssueNumber: "1", SortNumber: 1}

	// "Pornstar" is one continuous word — "star" appears only as a suffix
	// inside it, not as its own token. The substring bonus must not fire.
	result := newznab.SearchResult{
		Title: "Pornstar.Sweethearts.2024.XXX.1080p.HEVC.x265-PRT",
		Size:  500 * 1024 * 1024,
	}
	score := scoreResult(result, series, issue)
	if score >= 50 {
		t.Errorf("expected score < 50 for non-word-boundary match, got %d (title=%q, series=%q)",
			score, result.Title, series.Title)
	}
}
