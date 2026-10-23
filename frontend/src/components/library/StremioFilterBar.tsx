import { Film, Search, Tv, X } from "lucide-react";

export type LibrarySort = "name" | "imported_at" | "size" | "last_verified";
export type LibraryMediaFilter = "all" | "movie" | "series";

export function StremioFilterBar({
	search,
	onSearch,
	mediaType,
	onMediaType,
	genre,
	genres,
	onGenre,
	year,
	years,
	onYear,
	sortBy,
	onSort,
	resultCount,
	onClear,
	hasActiveFilters,
}: {
	search: string;
	onSearch: (value: string) => void;
	mediaType: LibraryMediaFilter;
	onMediaType: (value: LibraryMediaFilter) => void;
	genre: string;
	genres: string[];
	onGenre: (value: string) => void;
	year: string;
	years: string[];
	onYear: (value: string) => void;
	sortBy: LibrarySort;
	onSort: (value: LibrarySort) => void;
	resultCount: number;
	onClear: () => void;
	hasActiveFilters: boolean;
}) {
	return (
		<div className="space-y-4">
			<div className="flex flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-center">
				<label className="input flex w-full items-center gap-2 rounded-full sm:max-w-md sm:flex-1">
					<Search className="h-4 w-4 shrink-0 text-base-content/50" aria-hidden="true" />
					<input
						type="search"
						className="grow bg-transparent"
						aria-label="Search saved titles"
						placeholder="Search titles, year or IMDb ID…"
						value={search}
						onChange={(event) => onSearch(event.target.value)}
					/>
					{search && (
						<button
							type="button"
							className="btn btn-ghost btn-xs btn-circle"
							aria-label="Clear search"
							onClick={() => onSearch("")}
						>
							<X className="h-3.5 w-3.5" aria-hidden="true" />
						</button>
					)}
				</label>
				<fieldset className="grid w-full grid-cols-3 gap-1 rounded-full bg-base-200 p-1 sm:w-auto">
					<legend className="sr-only">Media type</legend>
					{(
						[
							{ value: "all", label: "All", icon: null },
							{ value: "movie", label: "Movies", icon: Film },
							{ value: "series", label: "Series", icon: Tv },
						] as const
					).map((option) => {
						const Icon = option.icon;
						const active = mediaType === option.value;
						return (
							<button
								key={option.value}
								type="button"
								aria-pressed={active}
								onClick={() => onMediaType(option.value)}
								className={`flex items-center justify-center gap-1.5 rounded-full px-4 py-1.5 font-medium text-sm transition-all duration-[180ms] ease-out motion-reduce:transition-none ${
									active
										? "bg-base-100 text-base-content shadow-sm"
										: "text-base-content/60 hover:text-base-content"
								}`}
							>
								{Icon && <Icon className="h-3.5 w-3.5" aria-hidden="true" />}
								{option.label}
							</button>
						);
					})}
				</fieldset>
			</div>
			<div className="flex flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-center">
				<select
					className="select select-sm w-full rounded-full sm:w-40"
					aria-label="Genre"
					value={genre}
					onChange={(event) => onGenre(event.target.value)}
				>
					<option value="all">All genres</option>
					{genres.map((value) => (
						<option key={value} value={value}>
							{value}
						</option>
					))}
				</select>
				<select
					className="select select-sm w-full rounded-full sm:w-32"
					aria-label="Year"
					value={year}
					onChange={(event) => onYear(event.target.value)}
				>
					<option value="all">All years</option>
					{years.map((value) => (
						<option key={value} value={value}>
							{value}
						</option>
					))}
				</select>
				<select
					className="select select-sm w-full rounded-full sm:w-44"
					aria-label="Sort by"
					value={sortBy}
					onChange={(event) => onSort(event.target.value as LibrarySort)}
				>
					<option value="name">Name</option>
					<option value="imported_at">Recently added</option>
					<option value="size">Largest</option>
					<option value="last_verified">Last verified</option>
				</select>
				<span className="text-base-content/60 text-xs sm:ml-auto">
					{resultCount} {resultCount === 1 ? "title" : "titles"}
				</span>
				{hasActiveFilters && (
					<button
						type="button"
						className="btn btn-ghost btn-xs gap-1 text-base-content/70"
						onClick={onClear}
					>
						<X className="h-3.5 w-3.5" aria-hidden="true" />
						Clear
					</button>
				)}
			</div>
		</div>
	);
}
