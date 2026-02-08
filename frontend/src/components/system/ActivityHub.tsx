import { Download, Play } from "lucide-react";
import { useState } from "react";
import { useActiveStreams, useQueue } from "../../hooks/useApi";
import { LoadingSpinner } from "../ui/LoadingSpinner";
import { StatusBadge } from "../ui/StatusBadge";
import { formatBytes, formatSpeed } from "../../lib/utils";

export function ActivityHub() {
	const [activeTab, setActiveTab] = useState<"playback" | "imports">("playback");
	const { data: streams, isLoading: streamsLoading } = useActiveStreams();
	const { data: queueItems, isLoading: queueLoading } = useQueue({ status: "processing", limit: 10 });

	const playbackCount = streams?.length || 0;
	const importCount = queueItems?.data?.length || 0;

	return (
		<div className="card bg-base-100 shadow-lg min-h-[400px]">
			<div className="card-body p-0">
				<div className="tabs tabs-bordered w-full grid grid-cols-2">
					<button
						type="button"
						className={`tab tab-lg gap-2 ${activeTab === "playback" ? "tab-active font-bold" : ""}`}
						onClick={() => setActiveTab("playback")}
					>
						<Play className="h-4 w-4" />
						Playback
						{playbackCount > 0 && <span className="badge badge-sm badge-primary">{playbackCount}</span>}
					</button>
					<button
						type="button"
						className={`tab tab-lg gap-2 ${activeTab === "imports" ? "tab-active font-bold" : ""}`}
						onClick={() => setActiveTab("imports")}
					>
						<Download className="h-4 w-4" />
						Imports
						{importCount > 0 && <span className="badge badge-sm badge-secondary">{importCount}</span>}
					</button>
				</div>

				<div className="p-4 overflow-y-auto max-h-[350px]">
					{activeTab === "playback" && (
						<div className="space-y-4">
							{streamsLoading ? (
								<LoadingSpinner />
							) : streams && streams.length > 0 ? (
								streams.map((stream) => (
									<div key={stream.id} className="border-b border-base-300 pb-3 last:border-0">
										<div className="flex justify-between items-start mb-1">
											<span className="font-medium text-sm truncate max-w-[70%]" title={stream.file_path}>
												{stream.file_path.split('/').pop()}
											</span>
											<span className="text-xs font-mono text-primary">{formatSpeed(stream.bytes_per_second)}</span>
										</div>
										<div className="flex justify-between text-xs text-base-content/60 mb-2">
											<span>{stream.user_name || "Anonymous"}</span>
											<span>{formatBytes(stream.current_offset)} / {formatBytes(stream.total_size)}</span>
										</div>
										<progress 
											className="progress progress-primary w-full h-1.5" 
											value={stream.current_offset} 
											max={stream.total_size}
										/>
									</div>
								))
							) : (
								<div className="text-center py-10 text-base-content/50">
									<Play className="h-8 w-8 mx-auto mb-2 opacity-20" />
									<p>No active streams</p>
								</div>
							)}
						</div>
					)}

					{activeTab === "imports" && (
						<div className="space-y-4">
							{queueLoading ? (
								<LoadingSpinner />
							) : queueItems?.data && queueItems.data.length > 0 ? (
								queueItems.data.map((item) => (
									<div key={item.id} className="border-b border-base-300 pb-3 last:border-0">
										<div className="flex justify-between items-start mb-1">
											<span className="font-medium text-sm truncate max-w-[75%]" title={item.nzb_path}>
												{item.target_path || item.nzb_path.split('/').pop()}
											</span>
											<StatusBadge status="processing" className="badge-sm" />
										</div>
										<div className="flex justify-between text-xs text-base-content/60 mb-2">
											<span>Worker #{item.id % 10}</span>
											<span>Attempt {item.retry_count + 1}</span>
										</div>
										{item.percentage !== undefined ? (
											<progress 
												className="progress progress-secondary w-full h-1.5" 
												value={item.percentage} 
												max="100"
											/>
										) : (
											<progress className="progress progress-secondary w-full h-1.5" />
										)}
									</div>
								))
							) : (
								<div className="text-center py-10 text-base-content/50">
									<Download className="h-8 w-8 mx-auto mb-2 opacity-20" />
									<p>No active imports</p>
								</div>
							)}
						</div>
					)}
				</div>
			</div>
		</div>
	);
}