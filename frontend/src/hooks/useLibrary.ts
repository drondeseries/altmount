import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "../api/client";
import type { LibraryMediaType } from "../types/library";

export function useLibrary(stremioOnly = false) {
	return useQuery({
		queryKey: ["saved-library", "titles", stremioOnly ? "stremio-digest" : "all"],
		queryFn: () => (stremioOnly ? apiClient.getLibraryDigest() : apiClient.getLibrary()),
	});
}

export function useRecentLibraryReleases(stremioOnly = false) {
	return useQuery({
		queryKey: ["saved-library", "recent", stremioOnly ? "stremio-only" : "all"],
		queryFn: () =>
			apiClient.getRecentLibraryReleases(stremioOnly ? { stremioOnly: true } : undefined),
	});
}

export function useLibraryDetail(type: LibraryMediaType, imdbID: string, enabled = true) {
	return useQuery({
		queryKey: ["saved-library", "detail", type, imdbID],
		queryFn: () => apiClient.getLibraryDetail(type, imdbID),
		enabled: enabled && !!imdbID,
	});
}

export function useLibraryActions() {
	const client = useQueryClient();
	const onSuccess = () => client.invalidateQueries({ queryKey: ["saved-library"] });
	const save = useMutation({
		mutationFn: (queueID: number) => apiClient.saveLibraryRelease(queueID),
		onSuccess,
	});
	const unsave = useMutation({
		mutationFn: (id: number) => apiClient.unsaveLibraryRelease(id),
		onSuccess,
	});
	const importHealth = useMutation({
		mutationFn: () => apiClient.importHealthyLibraryFiles(),
		onSuccess,
	});
	return { save, unsave, importHealth };
}
