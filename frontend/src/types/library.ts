export type LibraryMediaType = "movie" | "series";

export interface LibraryRelease {
	id: number;
	queue_id: number;
	name: string;
	imdb_id?: string;
	type: LibraryMediaType;
	season?: number;
	episode?: number;
	saved: boolean;
	expires_at: number;
	health?: string;
	size?: number;
	indexer?: string;
	instance?: string;
	category?: string;
	imported_at?: number;
	scene_name?: string;
	last_verified?: number;
	retry_count?: number;
	streaming_failures?: number;
	origin?: string;
	location?: string;
	tmdb_id?: number;
}

export interface LibraryEpisode {
	season: number;
	episode: number;
	title?: string;
	overview?: string;
	released?: string;
}

export interface LibraryMetadata {
	name?: string;
	poster?: string;
	background?: string;
	year?: string | number;
	releaseInfo?: string;
	description?: string;
	genres?: string[];
	cast?: string[];
	imdbRating?: string | number;
	videos?: LibraryEpisode[];
}

export interface LibraryTitleResponse {
	type: LibraryMediaType;
	imdb_id: string;
	name: string;
	metadata?: LibraryMetadata | null;
	releases: LibraryRelease[];
}

/** Display model keeps external metadata optional without hiding saved releases. */
export interface LibraryTitle {
	imdb_id: string;
	media_type: LibraryMediaType;
	title: string;
	poster?: string;
	background?: string;
	year?: string;
	description?: string;
	genres?: string[];
	cast?: string[];
	rating?: string;
	release_count: number;
	episodes?: LibraryEpisode[];
	releases: LibraryRelease[];
	/** Digest aggregates (present on the compact grid payload, absent on detail). */
	total_size?: number;
	max_imported_at?: number;
	max_last_verified?: number;
}

export type LibraryDetail = LibraryTitle;

/** Compact list payload from GET /library?digest=1: no metadata blob, no release rows. */
export interface LibraryDigestResponse {
	type: LibraryMediaType;
	imdb_id: string;
	title: string;
	poster?: string;
	background?: string;
	year?: string;
	rating?: string;
	genres?: string[];
	release_count: number;
	total_size: number;
	max_imported_at: number;
	max_last_verified: number;
}

export function libraryTitleFromResponse(value: LibraryTitleResponse): LibraryTitle {
	const meta = value.metadata || {};
	return {
		imdb_id: value.imdb_id,
		media_type: value.type,
		title: meta.name || value.name || value.imdb_id,
		poster: meta.poster,
		background: meta.background,
		year: String(meta.releaseInfo || meta.year || ""),
		description: meta.description,
		genres: meta.genres,
		cast: meta.cast,
		rating: meta.imdbRating ? String(meta.imdbRating) : undefined,
		episodes: meta.videos,
		release_count: value.releases?.length || 0,
		releases: value.releases || [],
	};
}

export function libraryTitleFromDigest(value: LibraryDigestResponse): LibraryTitle {
	return {
		imdb_id: value.imdb_id,
		media_type: value.type,
		title: value.title || value.imdb_id,
		poster: value.poster,
		background: value.background,
		year: value.year,
		genres: value.genres,
		rating: value.rating,
		release_count: value.release_count || 0,
		releases: [],
		total_size: value.total_size,
		max_imported_at: value.max_imported_at,
		max_last_verified: value.max_last_verified,
	};
}
