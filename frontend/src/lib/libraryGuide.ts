import type { LibraryEpisode, LibraryRelease, LibraryTitle } from "../types/library";

export function coveredEpisodes(releases: LibraryRelease[]): Set<string> {
	const covered = new Set<string>();
	for (const release of releases) {
		if (release.type === "series" && (release.season || 0) > 0 && (release.episode || 0) > 0) {
			covered.add(`${release.season}:${release.episode}`);
		}
	}
	return covered;
}

export function missingEpisodes(title: LibraryTitle): LibraryEpisode[] {
	if (title.media_type !== "series" || !title.episodes?.length) return [];
	const covered = coveredEpisodes(title.releases);
	return title.episodes.filter((e) => !covered.has(`${e.season}:${e.episode}`));
}

export function hasAuthoritativeGuide(title: LibraryTitle): boolean {
	return (
		title.media_type === "series" &&
		(title.episodes || []).length > 0 &&
		!title.imdb_id.startsWith("altm-")
	);
}
