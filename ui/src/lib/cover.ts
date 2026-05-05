// proxiedCoverURL routes a remote provider cover URL through the LongBox
// backend so the browser doesn't have to deal with referrer / hot-link blocks
// that ComicVine and Metron sometimes apply. Pass-through for empty / null /
// non-http URLs so callers can use it unconditionally.
export function proxiedCoverURL(url: string | null | undefined): string {
	if (!url) return '';
	if (!/^https?:/i.test(url)) return url;
	return `/api/v1/covers/proxy?u=${encodeURIComponent(url)}`;
}
