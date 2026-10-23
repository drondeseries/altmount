import { useQueryClient } from "@tanstack/react-query";
import {
	ArrowLeft,
	Bookmark,
	CalendarClock,
	Download,
	ExternalLink,
	Film,
	FolderOpen,
	Globe,
	HardDrive,
	Library,
	Search,
	Server,
	ShieldCheck,
	Star,
	Tag,
	Tv,
	X,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link, Navigate, useLocation, useParams, useSearchParams } from "react-router-dom";
import { apiClient } from "../api/client";
import { PosterCard } from "../components/library/PosterCard";
import { useConfirm } from "../contexts/ModalContext";
import { useConfig } from "../hooks/useConfig";
import {
	useLibrary,
	useLibraryActions,
	useLibraryDetail,
	useRecentLibraryReleases,
} from "../hooks/useLibrary";
import { coveredEpisodes, hasAuthoritativeGuide, missingEpisodes } from "../lib/libraryGuide";
import type { LibraryRelease, LibraryTitle } from "../types/library";

const healthRank: Record<string, number> = {
	healthy: 0,
	pending: 1,
	checking: 2,
	repair_triggered: 3,
	degraded: 4,
	corrupted: 5,
};

const healthBadgeClass: Record<string, string> = {
	healthy: "badge-success",
	pending: "badge-ghost",
	checking: "badge-ghost",
	repair_triggered: "badge-info",
	degraded: "badge-warning",
	corrupted: "badge-error",
};

const healthLabel: Record<string, string> = {
	healthy: "Healthy",
	pending: "Pending check",
	checking: "Checking",
	repair_triggered: "Repair queued",
	degraded: "Degraded",
	corrupted: "Corrupted",
};

function HealthBadge({ health }: { health?: string }) {
	const key = (health || "").toLowerCase();
	const label = healthLabel[key] || "Unknown";
	const cls = healthBadgeClass[key] || "badge-neutral";
	return (
		<span className={`badge ${cls} badge-sm gap-1`} title={`File health: ${label}`}>
			<span aria-hidden="true">●</span> {label}
		</span>
	);
}

const originBadgeClass: Record<string, string> = {
	stremio: "badge-info",
	arrs: "badge-accent",
	manual: "badge-ghost",
};

const originLabel: Record<string, string> = {
	stremio: "Stremio",
	arrs: "ARR",
	manual: "Manual",
};

function OriginTag({ origin }: { origin?: string }) {
	if (!origin) return null;
	const label = originLabel[origin] || origin;
	const cls = originBadgeClass[origin] || "badge-neutral";
	return (
		<span className={`badge ${cls} badge-xs`} title={`Source: ${label}`}>
			{label}
		</span>
	);
}

function worstHealth(releases: LibraryRelease[]): string {
	let worst = "";
	for (const release of releases) {
		const key = (release.health || "").toLowerCase();
		if (!(key in healthRank)) continue;
		if (worst === "" || healthRank[key] > healthRank[worst]) worst = key;
	}
	return worst;
}

function TitleBadges({ title }: { title: LibraryTitle }) {
	const guide = hasAuthoritativeGuide(title) ? title.episodes || [] : [];
	const missing = guide.length ? missingEpisodes(title).length : null;
	return (
		<div className="mt-1.5 flex flex-wrap gap-1.5">
			<HealthBadge health={worstHealth(title.releases)} />
			{missing === 0 && (title.release_count || 0) > 0 && (
				<span className="badge badge-success badge-sm">Complete</span>
			)}
		</div>
	);
}

function formatBytes(value?: number): string | null {
	if (!value || value <= 0) return null;
	const units = ["B", "KB", "MB", "GB", "TB"];
	let v = value;
	let i = 0;
	while (v >= 1024 && i < units.length - 1) {
		v /= 1024;
		i++;
	}
	return `${v >= 100 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

function qualityTags(name: string): string[] {
	const tags: string[] = [];
	const test = (re: RegExp, label: string) => {
		if (tags.length < 3 && re.test(name)) tags.push(label);
	};
	test(/\b2160p\b|\b4k\b|\buhd\b/i, "4K");
	test(/\b1080p\b/i, "1080p");
	test(/\b720p\b/i, "720p");
	test(/remux/i, "Remux");
	test(/\bblu-?ray\b/i, "Blu-ray");
	test(/web-?dl|webrip/i, "WEB");
	test(/\bdv\b|dolby.?vision/i, "DV");
	test(/hdr10\+?/i, "HDR10");
	test(/\bhdr\b/i, "HDR");
	test(/atmos|truehd/i, "Atmos");
	test(/\bdts\b/i, "DTS");
	return tags;
}

function Loading({ variant = "grid" }: { variant?: "grid" | "detail" }) {
	if (variant === "detail") {
		return (
			<output aria-label="Loading title" className="space-y-6">
				<div className="flex flex-col gap-6 rounded-2xl border border-base-300 bg-base-200 p-5 sm:flex-row sm:gap-8 sm:p-8">
					<div className="skeleton mx-auto aspect-[2/3] w-40 shrink-0 rounded-xl sm:mx-0 sm:w-52" />
					<div className="flex-1 space-y-4">
						<div className="skeleton h-4 w-24 rounded" />
						<div className="skeleton h-9 w-3/4 rounded" />
						<div className="skeleton h-6 w-1/2 rounded" />
						<div className="skeleton h-20 w-full rounded" />
					</div>
				</div>
				<div className="skeleton h-40 w-full rounded-2xl" />
				<span className="sr-only">Loading title…</span>
			</output>
		);
	}
	return (
		<output
			aria-label="Loading library"
			className="grid grid-cols-2 gap-x-4 gap-y-7 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6"
		>
			{Array.from({ length: 12 }).map((_, index) => (
				<div key={index} className="space-y-3">
					<div className="skeleton aspect-[2/3] w-full rounded-xl" />
					<div className="skeleton h-4 w-4/5 rounded" />
					<div className="skeleton h-3 w-2/5 rounded" />
				</div>
			))}
			<span className="sr-only">Loading library…</span>
		</output>
	);
}

function Failure({ error, retry }: { error: Error; retry: () => void }) {
	return (
		<div role="alert" className="alert alert-error flex flex-wrap justify-between">
			<span>{error.message || "Unable to load the library."}</span>
			<button type="button" className="btn btn-sm" onClick={retry}>
				Try again
			</button>
		</div>
	);
}

function Empty({ title, children }: { title: string; children: React.ReactNode }) {
	return (
		<div className="rounded-2xl border border-base-300 border-dashed px-6 py-16 text-center">
			<Library className="mx-auto mb-4 h-10 w-10 text-base-content/40" aria-hidden="true" />
			<h2 className="font-semibold text-xl">{title}</h2>
			<div className="mx-auto mt-2 max-w-lg text-base-content/70 text-sm leading-relaxed">
				{children}
			</div>
		</div>
	);
}

function Pager({
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
				className="btn btn-sm"
				disabled={page <= 1}
				onClick={() => onPage(page - 1)}
			>
				← Prev
			</button>
			{neighbors[0] > 1 && (
				<>
					<button type="button" className="btn btn-sm btn-ghost" onClick={() => onPage(1)}>
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
					className={`btn btn-sm ${p === page ? "btn-primary" : "btn-ghost"}`}
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
					<button type="button" className="btn btn-sm btn-ghost" onClick={() => onPage(pageCount)}>
						{pageCount}
					</button>
				</>
			)}
			<button
				type="button"
				className="btn btn-sm"
				disabled={page >= pageCount}
				onClick={() => onPage(page + 1)}
			>
				Next →
			</button>
		</nav>
	);
}

const GUIDE_PAGE = 60;

function GuideSection({ title }: { title: LibraryTitle }) {
	const episodes = title.episodes || [];
	const seasons = [...new Set(episodes.map((episode) => episode.season))].sort((a, b) => a - b);
	const [season, setSeason] = useState<number | "all">("all");
	const [shown, setShown] = useState(GUIDE_PAGE);
	const covered = coveredEpisodes(title.releases);
	const inSeason = season === "all" ? episodes : episodes.filter((e) => e.season === season);
	const missing = missingEpisodes(title);
	const saved = episodes.length - missing.length;
	return (
		<section className="space-y-4">
			<h2 className="font-semibold text-xl">Episode guide</h2>
			<p className="text-base-content/60 text-sm">
				{saved} of {episodes.length} episodes saved
				{missing.length ? ` · ${missing.length} missing` : " · complete"}. An episode listed here
				does not mean its release is saved.
			</p>
			{seasons.length > 1 && (
				<div role="tablist" aria-label="Seasons" className="flex flex-wrap gap-2">
					<button
						type="button"
						role="tab"
						aria-selected={season === "all"}
						className={`btn btn-sm ${season === "all" ? "btn-primary" : "btn-ghost"}`}
						onClick={() => {
							setSeason("all");
							setShown(GUIDE_PAGE);
						}}
					>
						All seasons
					</button>
					{seasons.map((value) => (
						<button
							key={value}
							type="button"
							role="tab"
							aria-selected={season === value}
							className={`btn btn-sm ${season === value ? "btn-primary" : "btn-ghost"}`}
							onClick={() => {
								setSeason(value);
								setShown(GUIDE_PAGE);
							}}
						>
							S{value}
						</button>
					))}
				</div>
			)}
			<ul className="grid gap-3 md:grid-cols-2">
				{inSeason.slice(0, shown).map((episode) => {
					const isSaved = covered.has(`${episode.season}:${episode.episode}`);
					return (
						<li
							key={`${episode.season}:${episode.episode}`}
							className="rounded-xl border border-base-300 p-4"
						>
							<span className="font-medium text-primary text-xs">
								Season {episode.season} · Episode {episode.episode}
							</span>{" "}
							<span className={`badge badge-xs ${isSaved ? "badge-success" : "badge-warning"}`}>
								{isSaved ? "Saved" : "Missing"}
							</span>
							<h3 className="mt-1 font-semibold">
								{episode.title || `Episode ${episode.episode}`}
							</h3>
							{episode.released && (
								<p className="mt-1 text-base-content/50 text-xs">{dateLabel(episode.released)}</p>
							)}
							{episode.overview && (
								<p className="mt-2 text-base-content/70 text-sm">{episode.overview}</p>
							)}
						</li>
					);
				})}
			</ul>
			{shown < inSeason.length && (
				<div className="flex justify-center">
					<button
						type="button"
						className="btn btn-sm btn-outline"
						onClick={() => setShown((n) => n + GUIDE_PAGE)}
					>
						Show more ({inSeason.length - shown} remaining)
					</button>
				</div>
			)}
		</section>
	);
}

const prettyCategory: Record<string, string> = {
	tv: "TV",
	movies: "Movies",
	movie: "Movies",
	series: "Series",
	music: "Music",
	audio: "Audio",
	books: "Books",
	ebooks: "Books",
};

function categoryLabel(value?: string): string | null {
	if (!value) return null;
	const key = value.toLowerCase();
	if (prettyCategory[key]) return prettyCategory[key];
	return value.charAt(0).toUpperCase() + value.slice(1);
}

function dateLabel(value?: string | number) {
	if (!value) return null;
	const date = new Date(typeof value === "number" ? value * 1000 : value);
	return Number.isNaN(date.getTime()) ? null : date.toLocaleString();
}

function ReleaseList({ releases, saved }: { releases: LibraryRelease[]; saved: boolean }) {
	const { save, unsave } = useLibraryActions();
	const { confirm } = useConfirm();
	const [notice, setNotice] = useState("");
	const mutation = saved ? unsave : save;
	const act = async (release: LibraryRelease) => {
		setNotice("");
		if (
			saved &&
			!(await confirm({
				title: "Unsave this release?",
				message:
					"This removes the release from your saved library, not an immediate delete of its files. It becomes temporary again and is eligible for cleanup after the configured grace period. Other saved releases are not affected.",
				confirmText: "Unsave release",
				type: "warning",
			}))
		)
			return;
		try {
			if (saved) await unsave.mutateAsync(release.id);
			else await save.mutateAsync(release.queue_id);
			setNotice(
				saved
					? "Release unsaved. Temporary retention now applies."
					: "Release saved to your library.",
			);
		} catch {
			/* Mutation error is displayed below. */
		}
	};
	return (
		<div className="space-y-3">
			{mutation.error && <Failure error={mutation.error} retry={() => mutation.reset()} />}
			<output className="block text-sm text-success">{notice}</output>
			<ul className="divide-y divide-base-300 overflow-hidden rounded-xl border border-base-300 bg-base-200/40">
				{releases.map((release) => (
					<li
						key={saved ? release.id : release.queue_id}
						className="flex flex-col gap-4 p-4 sm:flex-row sm:items-center sm:justify-between"
					>
						<div className="min-w-0">
							<p className="break-words font-medium text-sm">{release.name}</p>
							<div className="mt-2 flex flex-wrap items-center gap-1.5">
								<HealthBadge health={release.health} />
								<OriginTag origin={release.origin} />
								{qualityTags(release.name).map((tag) => (
									<span key={tag} className="badge badge-outline badge-xs">
										{tag}
									</span>
								))}
							</div>
							<div className="mt-2 grid grid-cols-1 gap-x-4 gap-y-1 text-base-content/60 text-xs min-[420px]:grid-cols-2">
								{formatBytes(release.size) && (
									<span className="flex items-center gap-1.5">
										<HardDrive className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
										{formatBytes(release.size)}
									</span>
								)}
								{release.indexer && (
									<span className="flex items-center gap-1.5">
										<Globe className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
										via {release.indexer}
									</span>
								)}
								{categoryLabel(release.category) && (
									<span className="flex items-center gap-1.5">
										<Tag className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
										{categoryLabel(release.category)}
									</span>
								)}
								{release.instance && (
									<span className="flex items-center gap-1.5">
										<Server className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
										{release.instance}
									</span>
								)}
								{release.location && (
									<span className="flex min-w-0 items-center gap-1.5" title={release.location}>
										<FolderOpen className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
										<span className="truncate">{release.location}</span>
									</span>
								)}
								{release.imported_at && (
									<span className="flex items-center gap-1.5">
										<CalendarClock className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
										imported {dateLabel(release.imported_at)}
									</span>
								)}
								{release.last_verified && (
									<span className="flex items-center gap-1.5">
										<ShieldCheck className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
										verified {dateLabel(release.last_verified)}
									</span>
								)}
								{release.type === "series" && release.season != null && release.season > 0 && (
									<span>
										Season {release.season}
										{release.episode != null && release.episode > 0
											? ` · Episode ${release.episode}`
											: ""}
									</span>
								)}
								{!saved && (
									<span className="text-warning">
										Temporary
										{dateLabel(release.expires_at)
											? ` · Cleanup eligible ${dateLabel(release.expires_at)}`
											: " · Subject to automatic cleanup"}
									</span>
								)}
							</div>
						</div>
						<button
							type="button"
							disabled={mutation.isPending}
							onClick={() => void act(release)}
							className={`btn btn-sm shrink-0 ${saved ? "btn-ghost" : "btn-primary"}`}
							aria-label={`${saved ? "Unsave" : "Save"} ${release.name}`}
						>
							{mutation.isPending &&
							mutation.variables === (saved ? release.id : release.queue_id) ? (
								<span className="loading loading-spinner loading-xs" />
							) : (
								<Bookmark className="h-4 w-4" aria-hidden="true" />
							)}
							{saved ? "Unsave" : "Save to library"}
						</button>
					</li>
				))}
			</ul>
		</div>
	);
}

function TitleReleasesSection({ title }: { title: LibraryTitle }) {
	const releases = title.releases || [];
	if (!releases.length) {
		return (
			<section className="space-y-3">
				<h2 className="font-semibold text-xl">Saved releases</h2>
				<Empty title="No saved releases">
					This title no longer has saved releases. You can save available temporary releases from
					the Recent tab.
				</Empty>
			</section>
		);
	}
	if (title.media_type !== "series") {
		return (
			<section className="space-y-3">
				<h2 className="font-semibold text-xl">
					Saved releases <span className="text-base-content/50">({releases.length})</span>
				</h2>
				<p className="text-base-content/70 text-sm">
					Every file saved under this movie. Unsave removes library protection, not the files
					immediately.
				</p>
				<ReleaseList releases={releases} saved />
			</section>
		);
	}
	const bySeason = new Map<number, Map<number, LibraryRelease[]>>();
	const ungrouped: LibraryRelease[] = [];
	for (const release of releases) {
		const season = release.season || 0;
		const episode = release.episode || 0;
		if (season <= 0 || episode <= 0) {
			ungrouped.push(release);
			continue;
		}
		let eps = bySeason.get(season);
		if (!eps) {
			eps = new Map();
			bySeason.set(season, eps);
		}
		const files = eps.get(episode) || [];
		files.push(release);
		eps.set(episode, files);
	}
	const seasons = [...bySeason.entries()].sort((a, b) => a[0] - b[0]);
	return (
		<section className="space-y-4">
			<h2 className="font-semibold text-xl">
				Saved releases <span className="text-base-content/50">({releases.length})</span>
			</h2>
			<p className="text-base-content/70 text-sm">
				Grouped Season &gt; Episode &gt; files. Unverified files without a season and episode stay
				listed separately.
			</p>
			{seasons.map(([season, eps]) => (
				<details
					key={season}
					className="collapse-arrow collapse rounded-xl border border-base-300 bg-base-200/40"
					open={seasons.length === 1}
				>
					<summary className="collapse-title font-semibold">Season {season}</summary>
					<div className="collapse-content space-y-4">
						{[...eps.entries()]
							.sort((a, b) => a[0] - b[0])
							.map(([episode, files]) => (
								<div key={episode} className="space-y-2">
									<h3 className="font-medium text-sm">
										Episode {episode}{" "}
										<span className="text-base-content/50">
											({files.length} {files.length === 1 ? "file" : "files"})
										</span>
									</h3>
									<ReleaseList releases={files} saved />
								</div>
							))}
					</div>
				</details>
			))}
			{ungrouped.length > 0 && (
				<div className="space-y-2">
					<h3 className="font-medium text-sm text-warning">
						Unverified ({ungrouped.length} {ungrouped.length === 1 ? "file" : "files"})
					</h3>
					<ReleaseList releases={ungrouped} saved />
				</div>
			)}
		</section>
	);
}

function ImportHealthButton() {
	const { importHealth } = useLibraryActions();
	const client = useQueryClient();
	const [notice, setNotice] = useState("");
	const [progress, setProgress] = useState("");
	const pollRef = useRef<number | null>(null);
	useEffect(
		() => () => {
			if (pollRef.current !== null) window.clearInterval(pollRef.current);
		},
		[],
	);
	const pollStatus = () => {
		if (pollRef.current !== null) window.clearInterval(pollRef.current);
		pollRef.current = window.setInterval(async () => {
			try {
				const status = await apiClient.getImportHealthStatus();
				if (status.running) {
					setProgress(
						`Importing… ${status.imported} imported so far (${status.skipped} already present).`,
					);
					return;
				}
				if (pollRef.current !== null) window.clearInterval(pollRef.current);
				pollRef.current = null;
				setProgress("");
				if (status.error) {
					setNotice(`Import stopped: ${status.error}`);
				} else {
					const refreshBits: string[] = [];
					if ((status.relinked || 0) > 0)
						refreshBits.push(
							`${status.relinked} ${status.relinked === 1 ? "title" : "titles"} resolved`,
						);
					if ((status.posters || 0) > 0)
						refreshBits.push(
							`${status.posters} ${status.posters === 1 ? "poster" : "posters"} refreshed`,
						);
					setNotice(
						`Imported ${status.imported} file${status.imported === 1 ? "" : "s"} into the library (${status.skipped} already present).` +
							(refreshBits.length ? ` Library refresh: ${refreshBits.join(", ")}.` : "") +
							(status.complete ? "" : " More files remain — run the import again to continue."),
					);
				}
				client.invalidateQueries({ queryKey: ["saved-library"] });
			} catch {
				/* Keep polling; transient failures resolve on the next tick. */
			}
		}, 5000);
	};
	const run = async () => {
		setNotice("");
		setProgress("");
		try {
			await importHealth.mutateAsync();
			setProgress("Import started in the background…");
			pollStatus();
		} catch {
			/* Mutation error is displayed below. */
		}
	};
	return (
		<div className="flex flex-wrap items-center gap-3">
			<button
				type="button"
				className="btn btn-sm btn-outline"
				disabled={importHealth.isPending}
				onClick={() => void run()}
			>
				{importHealth.isPending ? (
					<span className="loading loading-spinner loading-xs" />
				) : (
					<Download className="h-4 w-4" aria-hidden="true" />
				)}
				Import local library files
			</button>
			{importHealth.error && (
				<span role="alert" className="text-error text-sm">
					{importHealth.error.message || "Import failed."}
				</span>
			)}
			{progress && <output className="block text-info text-sm">{progress}</output>}
			{notice && <output className="block text-sm text-success">{notice}</output>}
		</div>
	);
}

export function LibraryPage({
	embedded = false,
	forceStremioOnly = false,
}: {
	embedded?: boolean;
	forceStremioOnly?: boolean;
} = {}) {
	const [params, setParams] = useSearchParams();
	const recent = !embedded && params.get("tab") === "recent";
	const [search, setSearch] = useState("");
	const [type, setType] = useState("all");
	const [genre, setGenre] = useState("all");
	const [year, setYear] = useState("all");
	const [indexer, setIndexer] = useState("all");
	const [category, setCategory] = useState("all");
	const [instance, setInstance] = useState("all");
	const [origin, setOrigin] = useState("all");
	const [stremioToggle, setStremioToggle] = useState(false);
	const stremioOnly = forceStremioOnly ? true : stremioToggle;
	const [sortBy, setSortBy] = useState("name");
	const titles = useLibrary(stremioOnly);
	const temporary = useRecentLibraryReleases(stremioOnly);
	const allTitles = titles.data || [];
	const allGenres = [...new Set(allTitles.flatMap((title) => title.genres || []))].sort();
	const allYears = [...new Set(allTitles.map((title) => title.year || "").filter(Boolean))]
		.sort()
		.reverse();
	const allIndexers = [
		...new Set(
			allTitles.flatMap((title) =>
				(title.releases || []).map((r) => r.indexer).filter((x): x is string => !!x),
			),
		),
	].sort();
	const allCategories = [
		...new Set(
			allTitles.flatMap((title) =>
				(title.releases || []).map((r) => r.category).filter((x): x is string => !!x),
			),
		),
	].sort();
	const allInstances = [
		...new Set(
			allTitles.flatMap((title) =>
				(title.releases || []).map((r) => r.instance).filter((x): x is string => !!x),
			),
		),
	].sort();
	const stats = {
		movies: allTitles.filter((title) => title.media_type === "movie").length,
		series: allTitles.filter((title) => title.media_type === "series").length,
		releases: allTitles.reduce((sum, title) => sum + (title.release_count || 0), 0),
		size: allTitles.reduce(
			(sum, title) =>
				sum + (title.releases || []).reduce((inner, release) => inner + (release.size || 0), 0),
			0,
		),
	};
	const titleMetric = (title: LibraryTitle, metric: "imported_at" | "size" | "last_verified") => {
		const values = (title.releases || []).map((r) =>
			metric === "size"
				? r.size || 0
				: metric === "imported_at"
					? r.imported_at || 0
					: r.last_verified || 0,
		);
		return metric === "size"
			? values.reduce((sum, v) => sum + v, 0)
			: values.reduce((max, v) => Math.max(max, v), 0);
	};
	const filtered = allTitles
		.filter(
			(title) =>
				(type === "all" || title.media_type === type) &&
				(genre === "all" || (title.genres || []).includes(genre)) &&
				(year === "all" || (title.year || "") === year) &&
				(indexer === "all" || (title.releases || []).some((r) => r.indexer === indexer)) &&
				(category === "all" || (title.releases || []).some((r) => r.category === category)) &&
				(instance === "all" || (title.releases || []).some((r) => r.instance === instance)) &&
				(origin === "all" || (title.releases || []).some((r) => r.origin === origin)) &&
				`${title.title} ${title.year || ""} ${title.imdb_id}`
					.toLowerCase()
					.includes(search.toLowerCase()),
		)
		.sort((a, b) => {
			switch (sortBy) {
				case "imported_at":
				case "size":
				case "last_verified":
					if (titleMetric(b, sortBy) !== titleMetric(a, sortBy)) {
						return titleMetric(b, sortBy) - titleMetric(a, sortBy);
					}
					break;
				default:
					break;
			}
			return a.title.toLowerCase().localeCompare(b.title.toLowerCase());
		});
	const releases = (temporary.data || []).filter(
		(release) =>
			!release.saved &&
			(type === "all" || release.type === type) &&
			`${release.name} ${release.imdb_id || ""}`.toLowerCase().includes(search.toLowerCase()),
	);
	const query = recent ? temporary : titles;
	const GRID_PAGE_SIZE = 48;
	const RECENT_PAGE_SIZE = 50;
	const rawPage = Number.parseInt(params.get("page") || "1", 10);
	const requestedPage = Number.isFinite(rawPage) && rawPage > 0 ? rawPage : 1;
	const setTab = (tab: "saved" | "recent") => {
		const next = new URLSearchParams(params);
		if (tab === "recent") next.set("tab", "recent");
		else next.delete("tab");
		next.delete("page");
		setParams(next);
	};
	const gotoPage = (page: number) => {
		const next = new URLSearchParams(params);
		if (page <= 1) next.delete("page");
		else next.set("page", String(page));
		setParams(next);
		document.getElementById("library-results")?.scrollIntoView({ block: "start" });
	};
	const gridPageCount = Math.max(1, Math.ceil(filtered.length / GRID_PAGE_SIZE));
	const gridPage = Math.min(requestedPage, gridPageCount);
	const visibleTitles = filtered.slice((gridPage - 1) * GRID_PAGE_SIZE, gridPage * GRID_PAGE_SIZE);
	const recentPageCount = Math.max(1, Math.ceil(releases.length / RECENT_PAGE_SIZE));
	const recentPage = Math.min(requestedPage, recentPageCount);
	const visibleReleases = releases.slice(
		(recentPage - 1) * RECENT_PAGE_SIZE,
		recentPage * RECENT_PAGE_SIZE,
	);
	return (
		<div className="space-y-6">
			<header className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
				<div className="min-w-0">
					{!embedded && (
						<div className="mb-2 flex items-center gap-2 text-primary">
							<Library className="h-5 w-5" aria-hidden="true" />
							<span className="font-medium text-sm">Your collection</span>
						</div>
					)}
					<h1 className="font-bold text-3xl">{embedded ? "Stremio Library" : "Library"}</h1>
					{!embedded && (
						<p className="mt-2 max-w-2xl text-base-content/70">
							Keep the releases you want. Stremio imports stay temporary until you choose to save
							them.
						</p>
					)}
				</div>
				{!embedded && (
					<div className="flex flex-wrap items-center gap-2 lg:justify-end">
						<span className="badge badge-ghost gap-1.5" title="Saved movies">
							<Film className="h-3.5 w-3.5" aria-hidden="true" />
							{stats.movies} {stats.movies === 1 ? "movie" : "movies"}
						</span>
						<span className="badge badge-ghost gap-1.5" title="Saved series">
							<Tv className="h-3.5 w-3.5" aria-hidden="true" />
							{stats.series} series
						</span>
						<span className="badge badge-ghost gap-1.5" title="Saved releases">
							{stats.releases} {stats.releases === 1 ? "release" : "releases"}
						</span>
						{formatBytes(stats.size) && (
							<span className="badge badge-ghost gap-1.5" title="Total size on disk">
								<HardDrive className="h-3.5 w-3.5" aria-hidden="true" />
								{formatBytes(stats.size)}
							</span>
						)}
						<ImportHealthButton />
					</div>
				)}
			</header>
			{!embedded && (
				<div role="tablist" aria-label="Library views" className="tabs tabs-box w-fit">
					<button
						type="button"
						role="tab"
						className={`tab ${!recent ? "tab-active" : ""}`}
						aria-selected={!recent}
						onClick={() => setTab("saved")}
					>
						Saved titles
					</button>
					<button
						type="button"
						role="tab"
						className={`tab ${recent ? "tab-active" : ""}`}
						aria-selected={recent}
						onClick={() => setTab("recent")}
					>
						Recent temporary NZBs
					</button>
				</div>
			)}
			<div className="rounded-2xl border border-base-300/70 bg-base-200/40 p-4">
				<div className="flex flex-col gap-3 sm:flex-row sm:flex-wrap">
					<label className="input flex w-full items-center gap-2 sm:max-w-md sm:flex-1">
						<Search className="h-4 w-4 text-base-content/50" aria-hidden="true" />
						<input
							type="search"
							className="grow"
							aria-label={recent ? "Search temporary releases" : "Search saved titles"}
							placeholder={recent ? "Search releases…" : "Search titles, year or IMDb ID…"}
							value={search}
							onChange={(event) => setSearch(event.target.value)}
						/>
					</label>
					<select
						className="select w-full sm:w-40"
						aria-label="Media type"
						value={type}
						onChange={(event) => setType(event.target.value)}
					>
						<option value="all">All types</option>
						<option value="movie">Movies</option>
						<option value="series">Series</option>
					</select>
					{!recent && (
						<>
							<select
								className="select w-full sm:w-44"
								aria-label="Genre"
								value={genre}
								onChange={(event) => setGenre(event.target.value)}
							>
								<option value="all">All genres</option>
								{allGenres.map((value) => (
									<option key={value} value={value}>
										{value}
									</option>
								))}
							</select>
							<select
								className="select w-full sm:w-32"
								aria-label="Year"
								value={year}
								onChange={(event) => setYear(event.target.value)}
							>
								<option value="all">All years</option>
								{allYears.map((value) => (
									<option key={value} value={value}>
										{value}
									</option>
								))}
							</select>
							{!embedded && (
								<>
									<select
										className="select w-full sm:w-40"
										aria-label="Indexer"
										value={indexer}
										onChange={(event) => setIndexer(event.target.value)}
									>
										<option value="all">All indexers</option>
										{allIndexers.map((value) => (
											<option key={value} value={value}>
												{value}
											</option>
										))}
									</select>
									<select
										className="select w-full sm:w-40"
										aria-label="Category"
										value={category}
										onChange={(event) => setCategory(event.target.value)}
									>
										<option value="all">All categories</option>
										{allCategories.map((value) => (
											<option key={value} value={value}>
												{value}
											</option>
										))}
									</select>
									<select
										className="select w-full sm:w-40"
										aria-label="Instance"
										value={instance}
										onChange={(event) => setInstance(event.target.value)}
									>
										<option value="all">All instances</option>
										{allInstances.map((value) => (
											<option key={value} value={value}>
												{value}
											</option>
										))}
									</select>
									<select
										className="select w-full sm:w-40"
										aria-label="Source"
										value={origin}
										onChange={(event) => setOrigin(event.target.value)}
									>
										<option value="all">All sources</option>
										<option value="stremio">Stremio</option>
										<option value="arrs">ARR</option>
										<option value="manual">Manual</option>
									</select>
								</>
							)}
							{!forceStremioOnly && (
								<label className="label cursor-pointer gap-2 px-2">
									<input
										type="checkbox"
										className="checkbox checkbox-sm checkbox-primary"
										checked={stremioToggle}
										onChange={(event) => setStremioToggle(event.target.checked)}
										aria-label="Stremio-only titles"
									/>
									<span
										className="label-text text-xs"
										title="Strictly Stremio-requested content with no ARR or manual counterpart"
									>
										Stremio-only
									</span>
								</label>
							)}
							<select
								className="select w-full sm:w-40"
								aria-label="Sort by"
								value={sortBy}
								onChange={(event) => setSortBy(event.target.value)}
							>
								<option value="name">Name</option>
								<option value="imported_at">Recently imported</option>
								<option value="size">Largest</option>
								<option value="last_verified">Last verified</option>
							</select>
						</>
					)}
				</div>
			</div>
			{recent && (
				<p className="rounded-xl bg-info/10 p-4 text-sm leading-relaxed">
					Completed Stremio imports are listed here while they are temporary.{" "}
					<strong>Save to library</strong> explicitly keeps a release. Browsing or playing a title
					does not save it.
				</p>
			)}
			{query.isPending ? (
				<Loading />
			) : query.error ? (
				<Failure error={query.error} retry={() => void query.refetch()} />
			) : recent ? (
				releases.length ? (
					<div id="library-results" className="scroll-mt-4 space-y-4">
						<ReleaseList releases={visibleReleases} saved={false} />
						<Pager page={recentPage} pageCount={recentPageCount} onPage={gotoPage} />
					</div>
				) : (
					<Empty
						title={search || type !== "all" ? "No matching releases" : "No temporary releases"}
					>
						Completed temporary Stremio NZBs will appear here. Try changing your filters if you
						expected a release.
					</Empty>
				)
			) : filtered.length ? (
				<>
					<div className="flex flex-wrap items-center gap-2 text-sm">
						<span className="text-base-content/60 text-xs">
							Showing {filtered.length} {filtered.length === 1 ? "title" : "titles"}
						</span>
						<span className="hidden text-base-content/30 sm:inline">·</span>
						<span className="text-base-content/60 text-xs">Filter by type</span>
						<button
							type="button"
							className={`btn btn-xs ${type === "movie" ? "btn-primary" : "btn-ghost"}`}
							aria-pressed={type === "movie"}
							title="Show movies only"
							onClick={() => {
								setType(type === "movie" ? "all" : "movie");
								document.getElementById("library-results")?.scrollIntoView({ block: "start" });
							}}
						>
							<Film className="h-3.5 w-3.5" aria-hidden="true" />
							Movies
						</button>
						<button
							type="button"
							className={`btn btn-xs ${type === "series" ? "btn-secondary" : "btn-ghost"}`}
							aria-pressed={type === "series"}
							title="Show series only"
							onClick={() => {
								setType(type === "series" ? "all" : "series");
								document.getElementById("library-results")?.scrollIntoView({ block: "start" });
							}}
						>
							<Tv className="h-3.5 w-3.5" aria-hidden="true" />
							Series
						</button>
						{(type !== "all" ||
							genre !== "all" ||
							year !== "all" ||
							indexer !== "all" ||
							category !== "all" ||
							instance !== "all" ||
							origin !== "all" ||
							stremioOnly ||
							search !== "") && (
							<button
								type="button"
								className="btn btn-xs btn-ghost gap-1 text-base-content/70"
								onClick={() => {
									setType("all");
									setGenre("all");
									setYear("all");
									setIndexer("all");
									setCategory("all");
									setInstance("all");
									setOrigin("all");
									setStremioToggle(false);
									setSearch("");
									if (embedded) {
										const next = new URLSearchParams(params);
										next.delete("page");
										next.delete("tab");
										setParams(next);
									} else {
										setParams({});
									}
								}}
							>
								<X className="h-3.5 w-3.5" aria-hidden="true" />
								Clear filters
							</button>
						)}
					</div>
					<div id="library-results" className="scroll-mt-4 space-y-4">
						<div className="grid grid-cols-2 gap-x-4 gap-y-7 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6">
							{visibleTitles.map((title) => (
								<Link
									key={`${title.media_type}:${title.imdb_id}`}
									to={`/library/${title.media_type}/${encodeURIComponent(title.imdb_id)}`}
									state={{ from: `${location.pathname}${location.search}` }}
									className="group rounded-xl outline-offset-4 focus-visible:outline-2 focus-visible:outline-primary"
								>
									<PosterCard title={title} />
									<h2 className="mt-3 line-clamp-2 font-semibold leading-snug transition-colors group-hover:text-primary">
										{title.title}
									</h2>
									<p className="mt-1 text-base-content/60 text-xs">
										{title.release_count
											? `${title.release_count} ${title.release_count === 1 ? "release" : "releases"}`
											: "No releases"}
										{(() => {
											const size = formatBytes(
												(title.releases || []).reduce((sum, r) => sum + (r.size || 0), 0),
											);
											return size ? ` · ${size}` : "";
										})()}
									</p>
									<TitleBadges title={title} />
								</Link>
							))}
						</div>
						<Pager page={gridPage} pageCount={gridPageCount} onPage={gotoPage} />
					</div>
				</>
			) : (
				<Empty
					title={
						search || type !== "all" || genre !== "all" || year !== "all"
							? "No matching titles"
							: "Make room for your favorites"
					}
				>
					{search || type !== "all" || genre !== "all" || year !== "all" ? (
						"Try a different search or filters."
					) : (
						<>
							Your saved movies and series will appear here.{" "}
							<button type="button" className="link link-primary" onClick={() => setTab("recent")}>
								Browse recent temporary NZBs
							</button>{" "}
							to save your first release.
						</>
					)}
				</Empty>
			)}
		</div>
	);
}

export function LibraryDetailPage() {
	const { type, imdbID = "" } = useParams();
	const location = useLocation();
	const { data: config } = useConfig();
	// Master-switch gate: the detail page never mounts (and never issues
	// library queries or artwork fetches) while the Stremio integration is
	// disabled or still resolving. The hook itself takes the gate as its
	// `enabled` argument — a useQuery subscription fetches regardless of the
	// rendered subtree, so gating only the render would be ineffective.
	const libraryEnabled = config?.stremio?.enabled === true;
	const query = useLibraryDetail(type === "series" ? "series" : "movie", imdbID, libraryEnabled);
	const title = query.data;
	if (config && !libraryEnabled) {
		return <Navigate to="/files" replace />;
	}
	if (!libraryEnabled) {
		return null;
	}
	// Detail is reachable from Files => Stremio Library (embedded) and the
	// legacy standalone route. Return to the entry surface; direct entries
	// fall back to the embedded view.
	const backTo =
		location.state && typeof location.state === "object" && "from" in location.state
			? String((location.state as { from?: string }).from || "/files?view=library")
			: "/files?view=library";
	return (
		<div className="space-y-6">
			<Link to={backTo} className="btn btn-ghost btn-sm rounded-full">
				<ArrowLeft className="h-4 w-4" aria-hidden="true" />
				Stremio Library
			</Link>
			{query.isPending ? (
				<Loading variant="detail" />
			) : query.error ? (
				<Failure error={query.error} retry={() => void query.refetch()} />
			) : (
				title && (
					<>
						<section className="relative isolate overflow-hidden rounded-[24px] border border-base-300/60 bg-base-200 shadow-[0_8px_32px_rgba(0,0,0,0.12)]">
							{(title.background || title.poster) && (
								<div
									aria-hidden="true"
									className="-z-10 pointer-events-none absolute inset-0 scale-105 blur-2xl saturate-150"
									style={{
										backgroundImage: `url(${JSON.stringify(title.background || title.poster)})`,
										backgroundSize: "cover",
										backgroundPosition: "center top",
									}}
								/>
							)}
							<div
								aria-hidden="true"
								className="-z-10 pointer-events-none absolute inset-0 bg-gradient-to-t from-base-200 via-base-200/85 to-base-200/30 backdrop-blur-[2px]"
							/>
							<div className="flex flex-col gap-6 p-5 sm:flex-row sm:gap-8 sm:p-8">
								<div className="mx-auto w-44 shrink-0 sm:mx-0 sm:w-56">
									<div className="overflow-hidden rounded-[18px] shadow-[0_16px_48px_rgba(0,0,0,0.28)] ring-1 ring-white/20">
										<PosterCard title={title} showTypeBadge={false} />
									</div>
								</div>
								<div className="min-w-0 flex-1">
									<div className="mb-3 flex items-center gap-2 text-primary">
										{title.media_type === "series" ? (
											<Tv className="h-4 w-4" />
										) : (
											<Film className="h-4 w-4" />
										)}
										<span className="font-medium text-sm">
											{title.media_type === "series" ? "Series" : "Movie"}
										</span>
									</div>
									<h1 className="font-bold text-3xl drop-shadow-sm sm:text-4xl">{title.title}</h1>
									<div className="mt-3 flex flex-wrap items-center gap-2">
										{title.year && (
											<span className="badge badge-neutral badge-sm">{title.year}</span>
										)}
										{title.rating && (
											<span className="badge badge-warning badge-sm gap-1">
												<Star className="h-3 w-3 fill-warning-content" aria-hidden="true" />
												{title.rating} / 10
											</span>
										)}
										{(() => {
											const totalSize = (title.releases || []).reduce(
												(sum, r) => sum + (r.size || 0),
												0,
											);
											const guide = title.media_type === "series" ? title.episodes || [] : [];
											const savedCount = guide.length
												? guide.length - missingEpisodes(title).length
												: null;
											const size = formatBytes(totalSize);
											return (
												<>
													<span className="badge badge-ghost badge-sm">
														{title.releases?.length || 0}{" "}
														{(title.releases?.length || 0) === 1 ? "release" : "releases"}
													</span>
													{size && <span className="badge badge-ghost badge-sm">{size}</span>}
													{savedCount != null && guide.length > 0 && (
														<span className="badge badge-ghost badge-sm">
															{savedCount} of {guide.length} episodes
														</span>
													)}
												</>
											);
										})()}
										{(() => {
											const releases = title.releases || [];
											const indexer = releases.find((r) => r.indexer)?.indexer;
											const instance = releases.find((r) => r.instance)?.instance;
											const category = releases.find((r) => r.category)?.category;
											const imported = releases
												.map((r) => r.imported_at)
												.filter((x): x is number => !!x)
												.sort((a, b) => b - a)[0];
											const verified = releases
												.map((r) => r.last_verified)
												.filter((x): x is number => !!x)
												.sort((a, b) => b - a)[0];
											return indexer || instance || category || imported || verified ? (
												<span className="flex flex-wrap items-center gap-1.5">
													{indexer && (
														<span className="badge badge-outline badge-xs gap-1 bg-base-100/60 backdrop-blur-sm">
															<Globe className="h-3 w-3" aria-hidden="true" />
															{indexer}
														</span>
													)}
													{categoryLabel(category) && (
														<span className="badge badge-outline badge-xs gap-1 bg-base-100/60 backdrop-blur-sm">
															<Tag className="h-3 w-3" aria-hidden="true" />
															{categoryLabel(category)}
														</span>
													)}
													{instance && (
														<span className="badge badge-outline badge-xs gap-1 bg-base-100/60 backdrop-blur-sm">
															<Server className="h-3 w-3" aria-hidden="true" />
															{instance}
														</span>
													)}
													{imported && (
														<span className="badge badge-ghost badge-xs gap-1">
															<CalendarClock className="h-3 w-3" aria-hidden="true" />
															imported {dateLabel(imported)}
														</span>
													)}
													{verified && (
														<span className="badge badge-ghost badge-xs gap-1">
															<ShieldCheck className="h-3 w-3" aria-hidden="true" />
															verified {dateLabel(verified)}
														</span>
													)}
													{(() => {
														const location = (title.releases || [])
															.map((r) => r.location)
															.find(Boolean);
														return location ? (
															<span
																className="flex min-w-0 items-center gap-1.5 text-xs"
																title={location}
															>
																<FolderOpen className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
																<span className="truncate">{location}</span>
															</span>
														) : null;
													})()}
												</span>
											) : null;
										})()}
										<a
											className="link link-primary text-sm"
											href={`https://www.imdb.com/title/${encodeURIComponent(title.imdb_id)}/`}
											target="_blank"
											rel="noopener noreferrer"
										>
											IMDb <span className="sr-only">(opens in a new tab)</span>↗
										</a>
										{(() => {
											const tmdb = (title.releases || []).map((r) => r.tmdb_id).find(Boolean);
											return tmdb ? (
												<a
													className="link link-primary text-sm"
													href={`https://www.themoviedb.org/movie/${tmdb}`}
													target="_blank"
													rel="noopener noreferrer"
												>
													TMDB <ExternalLink className="h-3 w-3" aria-hidden="true" />
												</a>
											) : null;
										})()}
									</div>
									{!!title.genres?.length && (
										<div className="mt-4 flex flex-wrap gap-2">
											{title.genres.map((genre) => (
												<span key={genre} className="badge badge-outline">
													{genre}
												</span>
											))}
										</div>
									)}
									<p className="mt-5 max-w-3xl text-base-content/80 leading-relaxed">
										{title.description || "A synopsis is not available for this title yet."}
									</p>
									{!!title.cast?.length && (
										<div className="mt-5">
											<h2 className="font-semibold text-sm">Cast</h2>
											<p className="mt-1 text-base-content/70 text-sm leading-relaxed">
												{title.cast.join(" · ")}
											</p>
										</div>
									)}
								</div>
							</div>
						</section>
						<TitleReleasesSection title={title} />
						{hasAuthoritativeGuide(title) && <GuideSection title={title} />}
					</>
				)
			)}
		</div>
	);
}
