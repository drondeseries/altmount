import { useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useLibrary } from "../../hooks/useLibrary";
import type { LibraryTitle } from "../../types/library";
import { type LibraryMediaFilter, type LibrarySort, StremioFilterBar } from "./StremioFilterBar";
import { StremioEmpty, StremioGrid, StremioLoading } from "./StremioGrid";

const GRID_PAGE_SIZE = 48;

// Aggregates come from the compact digest payload when present; full-payload
// callers (standalone library page) reduce over the release list instead.
function titleSize(title: LibraryTitle): number {
	return title.total_size ?? (title.releases || []).reduce((sum, r) => sum + (r.size || 0), 0);
}

function titleMetric(title: LibraryTitle, metric: "imported_at" | "last_verified"): number {
	if (metric === "imported_at") {
		return (
			title.max_imported_at ??
			(title.releases || []).reduce((max, r) => Math.max(max, r.imported_at || 0), 0)
		);
	}
	return (
		title.max_last_verified ??
		(title.releases || []).reduce((max, r) => Math.max(max, r.last_verified || 0), 0)
	);
}

function StremioPager({
	page,
	pageCount,
	onPage,
}: {
	page: number;
	pageCount: number;
	onPage: (page: number) => void;
}) {
	if (pageCount <= 1) return null;
	const neighbors = [page - 1, page, page + 1].filter((p) => p >= 1 && p <= pageCount);
	return (
		<nav aria-label="Pagination" className="flex flex-wrap items-center justify-center gap-2">
			<button
				type="button"
				className="btn btn-sm rounded-full"
				disabled={page <= 1}
				onClick={() => onPage(page - 1)}
			>
				← Prev
			</button>
			{neighbors[0] > 1 && (
				<>
					<button
						type="button"
						className="btn btn-sm btn-ghost rounded-full"
						onClick={() => onPage(1)}
					>
						1
					</button>
					{neighbors[0] > 2 && <span className="text-base-content/40">…</span>}
				</>
			)}
			{neighbors.map((p) => (
				<button
					key={p}
					type="button"
					aria-current={p === page ? "page" : undefined}
					className={`btn btn-sm rounded-full ${p === page ? "btn-primary" : "btn-ghost"}`}
					onClick={() => onPage(p)}
				>
					{p}
				</button>
			))}
			{neighbors[neighbors.length - 1] < pageCount && (
				<>
					{neighbors[neighbors.length - 1] < pageCount - 1 && (
						<span className="text-base-content/40">…</span>
					)}
					<button
						type="button"
						className="btn btn-sm btn-ghost rounded-full"
						onClick={() => onPage(pageCount)}
					>
						{pageCount}
					</button>
				</>
			)}
			<button
				type="button"
				className="btn btn-sm rounded-full"
				disabled={page >= pageCount}
				onClick={() => onPage(page + 1)}
			>
				Next →
			</button>
		</nav>
	);
}

/**
 * Embedded Stremio-only library for Files => Stremio Library.
 * Always requests stremio_only=1; standalone LibraryPage is untouched.
 */
export function StremioLibrary() {
	const [params, setParams] = useSearchParams();
	const [search, setSearch] = useState("");
	const [mediaType, setMediaType] = useState<LibraryMediaFilter>("all");
	const [genre, setGenre] = useState("all");
	const [year, setYear] = useState("all");
	const [sortBy, setSortBy] = useState<LibrarySort>("name");

	// Stremio-only invariant: forced, no toggle.
	const titles = useLibrary(true);
	const allTitles = titles.data || [];

	const genres = useMemo(
		() => [...new Set(allTitles.flatMap((title) => title.genres || []))].sort(),
		[allTitles],
	);
	const years = useMemo(
		() => [...new Set(allTitles.map((title) => title.year || "").filter(Boolean))].sort().reverse(),
		[allTitles],
	);

	const filtered = useMemo(() => {
		const needle = search.toLowerCase();
		return allTitles
			.filter(
				(title) =>
					(mediaType === "all" || title.media_type === mediaType) &&
					(genre === "all" || (title.genres || []).includes(genre)) &&
					(year === "all" || (title.year || "") === year) &&
					`${title.title} ${title.year || ""} ${title.imdb_id}`.toLowerCase().includes(needle),
			)
			.sort((a, b) => {
				switch (sortBy) {
					case "imported_at":
					case "last_verified":
						if (titleMetric(b, sortBy) !== titleMetric(a, sortBy)) {
							return titleMetric(b, sortBy) - titleMetric(a, sortBy);
						}
						break;
					case "size":
						if (titleSize(b) !== titleSize(a)) return titleSize(b) - titleSize(a);
						break;
					default:
						break;
				}
				return a.title.toLowerCase().localeCompare(b.title.toLowerCase());
			});
	}, [allTitles, search, mediaType, genre, year, sortBy]);

	const setSearchResetPage = (value: string) => {
		setSearch(value);
		const next = new URLSearchParams(params);
		next.delete("page");
		setParams(next, { replace: true });
	};
	const setMediaTypeResetPage = (value: typeof mediaType) => {
		setMediaType(value);
		const next = new URLSearchParams(params);
		next.delete("page");
		setParams(next, { replace: true });
	};
	const setGenreResetPage = (value: string) => {
		setGenre(value);
		const next = new URLSearchParams(params);
		next.delete("page");
		setParams(next, { replace: true });
	};
	const setYearResetPage = (value: string) => {
		setYear(value);
		const next = new URLSearchParams(params);
		next.delete("page");
		setParams(next, { replace: true });
	};
	const rawPage = Number.parseInt(params.get("page") || "1", 10);
	const requestedPage = Number.isFinite(rawPage) && rawPage > 0 ? rawPage : 1;
	const gotoPage = (page: number) => {
		const next = new URLSearchParams(params);
		if (page <= 1) next.delete("page");
		else next.set("page", String(page));
		setParams(next);
		document.getElementById("library-results")?.scrollIntoView({ block: "start" });
	};
	const pageCount = Math.max(1, Math.ceil(filtered.length / GRID_PAGE_SIZE));
	const page = Math.min(requestedPage, pageCount);
	const visible = filtered.slice((page - 1) * GRID_PAGE_SIZE, page * GRID_PAGE_SIZE);

	const hasActiveFilters =
		search !== "" || mediaType !== "all" || genre !== "all" || year !== "all";
	const clearFilters = () => {
		setSearch("");
		setMediaType("all");
		setGenre("all");
		setYear("all");
		const next = new URLSearchParams(params);
		next.delete("page");
		setParams(next);
	};

	return (
		<div className="space-y-6">
			{titles.data && (
				<p className="text-base-content/60 text-sm">
					{allTitles.length} {allTitles.length === 1 ? "title" : "titles"} saved
				</p>
			)}
			<StremioFilterBar
				search={search}
				onSearch={setSearchResetPage}
				mediaType={mediaType}
				onMediaType={setMediaTypeResetPage}
				genre={genre}
				genres={genres}
				onGenre={setGenreResetPage}
				year={year}
				years={years}
				onYear={setYearResetPage}
				sortBy={sortBy}
				onSort={setSortBy}
				resultCount={filtered.length}
				onClear={clearFilters}
				hasActiveFilters={hasActiveFilters}
			/>
			{titles.isPending ? (
				<StremioLoading />
			) : titles.error ? (
				<div
					role="alert"
					className="alert alert-error flex flex-wrap justify-between rounded-[18px]"
				>
					<span>{titles.error.message || "Unable to load the library."}</span>
					<button
						type="button"
						className="btn btn-sm rounded-full"
						onClick={() => void titles.refetch()}
					>
						Try again
					</button>
				</div>
			) : filtered.length ? (
				<div className="space-y-6">
					<StremioGrid titles={visible} />
					<StremioPager page={page} pageCount={pageCount} onPage={gotoPage} />
				</div>
			) : (
				<StremioEmpty hasFilters={hasActiveFilters} />
			)}
		</div>
	);
}
