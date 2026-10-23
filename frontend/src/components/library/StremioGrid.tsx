import { Library } from "lucide-react";
import { Link, useLocation } from "react-router-dom";
import type { LibraryTitle } from "../../types/library";
import { PosterCard } from "./PosterCard";

export function StremioGrid({ titles }: { titles: LibraryTitle[] }) {
	const location = useLocation();
	if (!titles.length) return null;
	// 48 tiles render per page; priority-fetched above-the-fold row loads its
	// artwork first, everything else stays lazy. Tiles queue via
	// content-visibility so offscreen rows skip layout/paint work entirely.
	return (
		<div
			id="library-results"
			className="grid scroll-mt-4 grid-cols-2 gap-x-4 gap-y-8 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6"
		>
			{titles.map((title, index) => (
				<Link
					key={`${title.media_type}:${title.imdb_id}`}
					to={`/library/${title.media_type}/${encodeURIComponent(title.imdb_id)}`}
					state={{ from: `${location.pathname}${location.search}` }}
					style={{ contentVisibility: "auto", containIntrinsicSize: "auto 380px" }}
					className="group rounded-[18px] outline-offset-4 focus-visible:outline-2 focus-visible:outline-primary"
				>
					<PosterCard title={title} priority={index < 8} />
					<h2 className="mt-2.5 line-clamp-1 font-semibold text-[15px] leading-snug transition-colors duration-[180ms] group-hover:text-primary motion-reduce:transition-none">
						{title.title}
					</h2>
					<p className="mt-0.5 text-[13px] text-base-content/60">
						{title.year || (title.media_type === "series" ? "Series" : "Movie")}
						{title.rating ? ` · ★ ${title.rating}` : ""}
					</p>
				</Link>
			))}
		</div>
	);
}

export function StremioLoading() {
	return (
		<output
			aria-label="Loading library"
			className="grid grid-cols-2 gap-x-4 gap-y-8 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6"
		>
			{Array.from({ length: 12 }).map((_, index) => (
				<div key={index} className="space-y-2.5">
					<div className="skeleton aspect-[2/3] w-full rounded-[18px]" />
					<div className="skeleton h-4 w-4/5 rounded" />
					<div className="skeleton h-3 w-2/5 rounded" />
				</div>
			))}
			<span className="sr-only">Loading library…</span>
		</output>
	);
}

export function StremioEmpty({
	hasFilters,
	onBrowseRecent,
}: {
	hasFilters: boolean;
	onBrowseRecent?: () => void;
}) {
	return (
		<div className="rounded-[18px] border border-base-300 border-dashed px-6 py-16 text-center">
			<Library className="mx-auto mb-4 h-10 w-10 text-base-content/40" aria-hidden="true" />
			<h2 className="font-semibold text-xl">
				{hasFilters ? "No matching titles" : "Make room for your favorites"}
			</h2>
			<div className="mx-auto mt-2 max-w-lg text-base-content/70 text-sm leading-relaxed">
				{hasFilters ? (
					"Try a different search or filters."
				) : (
					<>
						Your saved movies and series will appear here.{" "}
						{onBrowseRecent && (
							<>
								<button type="button" className="link link-primary" onClick={onBrowseRecent}>
									Browse recent temporary NZBs
								</button>{" "}
								to save your first release.
							</>
						)}
					</>
				)}
			</div>
		</div>
	);
}
