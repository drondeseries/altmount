import { Library, Save } from "lucide-react";
import { useEffect, useState } from "react";
import type { ConfigResponse, LibraryConfig } from "../../types/config";
import { LoadingSpinner } from "../ui/LoadingSpinner";

interface LibraryConfigSectionProps {
	config: ConfigResponse;
	onUpdate?: (section: string, data: LibraryConfig) => Promise<void>;
	isReadOnly?: boolean;
	isUpdating?: boolean;
}

function initializeLibraryFormData(config: ConfigResponse): LibraryConfig {
	const library = config.library;
	return {
		auto_save: library?.auto_save || config.stremio?.library_auto_save || "off",
		auto_import_health:
			library?.auto_import_health ?? config.stremio?.library_auto_import_health ?? false,
	};
}

export function LibraryConfigSection({
	config,
	onUpdate,
	isReadOnly = false,
	isUpdating = false,
}: LibraryConfigSectionProps) {
	const [formData, setFormData] = useState<LibraryConfig>(() => initializeLibraryFormData(config));
	const [initialSnapshot, setInitialSnapshot] = useState<string>(() =>
		JSON.stringify(initializeLibraryFormData(config)),
	);
	const [hasChanges, setHasChanges] = useState(false);
	const [saveError, setSaveError] = useState<string | null>(null);

	useEffect(() => {
		const initialized = initializeLibraryFormData(config);
		setFormData(initialized);
		setInitialSnapshot(JSON.stringify(initialized));
		setHasChanges(false);
		setSaveError(null);
	}, [config]);

	const updateFormData = (patch: Partial<LibraryConfig>) => {
		setFormData((prev) => {
			const updated = { ...prev, ...patch };
			setHasChanges(JSON.stringify(updated) !== initialSnapshot);
			return updated;
		});
	};

	const handleSave = async () => {
		if (!onUpdate || !hasChanges) return;
		setSaveError(null);
		try {
			await onUpdate("library", formData);
			setInitialSnapshot(JSON.stringify(formData));
			setHasChanges(false);
		} catch (e) {
			setSaveError(e instanceof Error ? e.message : "Failed to save library configuration");
		}
	};

	// Values inherited from the old Stremio-level keys migrate on save.
	const usingLegacyValues =
		(!config.library?.auto_save && config.stremio?.library_auto_save) ||
		(config.library?.auto_import_health === undefined &&
			config.stremio?.library_auto_import_health !== undefined);

	return (
		<div className="mx-auto min-w-0 max-w-4xl space-y-6">
			<div className="rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
				<div className="flex items-center gap-3">
					<div className="flex h-10 w-10 items-center justify-center rounded-xl bg-primary text-primary-content shadow-sm">
						<Library className="h-5 w-5" />
					</div>
					<div>
						<h2 className="font-extrabold text-lg uppercase tracking-tight">Saved Library</h2>
						<p className="text-base-content/60 text-xs">
							Automatic pinning of completed NZBs and healthy local files into the browsable
							collection
						</p>
					</div>
				</div>

				<div className="mt-5 space-y-6">
					{usingLegacyValues && (
						<p className="rounded-xl bg-info/10 p-4 text-sm leading-relaxed">
							These settings were previously stored under Stremio. Saving here migrates them to the
							library section.
						</p>
					)}

					<fieldset className="fieldset min-w-0">
						<legend className="fieldset-legend font-semibold text-xs">Auto-Save</legend>
						<select
							className="select select-bordered w-full min-w-0 text-xs"
							value={formData.auto_save ?? "off"}
							disabled={isReadOnly}
							onChange={(e) => updateFormData({ auto_save: e.target.value })}
						>
							<option value="off">Off — save explicitly only</option>
							<option value="stremio">Stremio NZBs only</option>
							<option value="arrs">ARR imports only (Sonarr/Radarr via SABnzbd)</option>
							<option value="all">All completed NZBs</option>
						</select>
						<p className="label text-[11px] text-base-content/50">
							Automatically pin completed imports into the saved library, protecting them from TTL
							cleanup. Saved items accumulate until unsaved or deleted.
						</p>
					</fieldset>

					<label className="flex cursor-pointer items-start justify-between gap-3 rounded-xl border border-base-300 bg-base-100/70 p-4">
						<span className="min-w-0 flex-1">
							<span className="font-semibold text-sm">Auto-import local library files</span>
							<span className="mt-1 block text-base-content/60 text-xs">
								Hourly pin healthy local files (your arr-managed collection) into the saved library
								so they stay browsable with details.
							</span>
						</span>
						<input
							type="checkbox"
							className="toggle toggle-primary shrink-0"
							checked={formData.auto_import_health ?? false}
							disabled={isReadOnly}
							onChange={(e) => updateFormData({ auto_import_health: e.target.checked })}
						/>
					</label>

					{saveError && (
						<div role="alert" className="alert alert-error">
							<span>{saveError}</span>
						</div>
					)}
				</div>
			</div>

			{!isReadOnly && (
				<div className="flex justify-end border-base-200 border-t pt-4">
					<button
						type="button"
						className={`btn btn-primary px-10 shadow-lg shadow-primary/20 ${!hasChanges && "btn-ghost border-base-300"}`}
						onClick={handleSave}
						disabled={!hasChanges || isUpdating}
					>
						{isUpdating ? <LoadingSpinner size="sm" /> : <Save className="h-4 w-4" />}
						{isUpdating ? "Saving..." : "Save Changes"}
					</button>
				</div>
			)}
		</div>
	);
}
