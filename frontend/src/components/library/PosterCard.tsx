import { useState } from "react";
import type { LibraryTitle } from "../../types/library";

/**
 * Apple-style deterministic artwork. Guarantees every tile has a poster
 * even when the provider has no art for the title (~27% of the library).
 * Derived from the IMDb ID so it is stable across renders and sessions.
 */
const PAIRINGS: Array<[string, string]> = [
	["#0a2540", "#635bff"],
	["#1a1b3a", "#7c3aed"],
	["#0b3d2e", "#10b981"],
	["#3b0764", "#db2777"],
	["#172554", "#0ea5e9"],
	["#431407", "#f59e0b"],
	["#111827", "#6366f1"],
	["#022c22", "#14b8a6"],
	["#2e1065", "#8b5cf6"],
	["#450a0a", "#ef4444"],
	["#082f49", "#38bdf8"],
	["#311c0e", "#f97316"],
];

export function generatedGradient(seed: string): { from: string; to: string } {
	let hash = 2166136261;
	for (let i = 0; i < seed.length; i++) {
		hash ^= seed.charCodeAt(i);
		hash = Math.imul(hash, 16777619);
	}
	const [from, to] = PAIRINGS[Math.abs(hash) % PAIRINGS.length];
	return { from, to };
}

function initials(name: string): string {
	const words = name
		.replace(/^the\s+/i, "")
		.split(/[\s:–—-]+/)
		.filter(Boolean)
		.slice(0, 2);
	if (words.length === 0) return "?";
	if (words.length === 1) return words[0].slice(0, 2).toUpperCase();
	return (words[0][0] + words[1][0]).toUpperCase();
}

export function GeneratedArtwork({
	title,
	compact = false,
}: {
	title: LibraryTitle;
	compact?: boolean;
}) {
	const { from, to } = generatedGradient(title.imdb_id || title.title);
	return (
		<div
			aria-hidden="true"
			className="absolute inset-0"
			style={{ background: `linear-gradient(150deg, ${from} 0%, ${to} 135%)` }}
		>
			<div
				className="absolute inset-0 opacity-40"
				style={{
					background:
						"radial-gradient(120% 90% at 15% 10%, rgba(255,255,255,0.35) 0%, rgba(255,255,255,0) 45%), radial-gradient(120% 120% at 85% 90%, rgba(0,0,0,0.45) 0%, rgba(0,0,0,0) 55%)",
				}}
			/>
			<div className="absolute inset-0 flex flex-col items-center justify-center gap-1 p-4 text-center text-white">
				<span
					className={`font-bold tracking-tight drop-shadow-lg ${compact ? "text-4xl" : "text-6xl"}`}
				>
					{initials(title.title)}
				</span>
				<span
					className={`line-clamp-2 font-semibold leading-tight opacity-95 drop-shadow ${
						compact ? "text-xs" : "text-sm"
					}`}
				>
					{title.title}
				</span>
				{title.year && (
					<span className={`opacity-80 drop-shadow ${compact ? "text-[10px]" : "text-xs"}`}>
						{title.year}
					</span>
				)}
			</div>
		</div>
	);
}

export function PosterCard({
	title,
	showTypeBadge = true,
	priority = false,
}: {
	title: LibraryTitle;
	showTypeBadge?: boolean;
	priority?: boolean;
}) {
	const src = (title.poster || "").trim();
	const [failed, setFailed] = useState(false);
	const [loaded, setLoaded] = useState(false);
	const [seenSrc, setSeenSrc] = useState(src);
	const showImage = src !== "" && !failed;

	// Reset load state when the artwork URL changes (retries, backfill).
	// A stalled request keeps the generated art visible behind the image: the
	// card always shows art immediately for blank/failed URLs and fades the
	// real image over it, so blocked queues never blank a tile.
	if (seenSrc !== src) {
		setSeenSrc(src);
		setFailed(false);
		setLoaded(false);
	}

	return (
		<div className="motion-safe:group-hover:-translate-y-0.5 relative aspect-[2/3] overflow-hidden rounded-[18px] bg-base-300 shadow-[0_1px_2px_rgba(0,0,0,0.08)] ring-1 ring-black/5 transition-all duration-[180ms] ease-out motion-safe:group-hover:shadow-[0_8px_24px_rgba(0,0,0,0.16)] motion-reduce:transition-none dark:ring-white/10">
			{/* Generated art always renders underneath: blank/failed URLs show
			    art immediately, real images fade over it once loaded. */}
			<GeneratedArtwork title={title} />
			{showImage && (
				<img
					src={src}
					alt=""
					loading={priority ? "eager" : "lazy"}
					fetchPriority={priority ? "high" : "auto"}
					decoding="async"
					onLoad={() => setLoaded(true)}
					onError={() => setFailed(true)}
					className={`absolute inset-0 h-full w-full object-cover transition-all duration-[180ms] ease-out motion-safe:group-hover:scale-[1.015] motion-reduce:transition-none ${
						loaded ? "opacity-100" : "opacity-0"
					}`}
				/>
			)}
			<div
				aria-hidden="true"
				className="pointer-events-none absolute inset-x-0 bottom-0 h-2/5 bg-gradient-to-t from-black/70 to-transparent"
			/>
			{showTypeBadge && (
				<span className="absolute top-3 left-3 rounded-md bg-black/55 px-2 py-1 font-medium text-white text-xs backdrop-blur-md">
					{title.media_type === "series" ? "Series" : "Movie"}
				</span>
			)}
			{(title.rating || title.year) && (
				<div className="absolute inset-x-3 bottom-3 flex items-center justify-between text-white">
					{title.rating ? (
						<span className="flex items-center gap-1 font-semibold text-sm drop-shadow">
							<span aria-hidden="true" className="text-amber-300">
								★
							</span>
							<span className="sr-only">Rated&nbsp;</span>
							{title.rating}
						</span>
					) : (
						<span />
					)}
					{title.year && <span className="text-xs opacity-90 drop-shadow">{title.year}</span>}
				</div>
			)}
		</div>
	);
}
