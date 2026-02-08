import { CheckCircle2, History } from "lucide-react";
import { useQueue } from "../../hooks/useApi";
import { formatRelativeTime } from "../../lib/utils";
import { LoadingSpinner } from "../ui/LoadingSpinner";

export function RecentCompletions() {
	const { data: queue, isLoading } = useQueue({ status: "completed", limit: 10 });

	if (isLoading) return <LoadingSpinner size="sm" />;
	if (!queue?.data || queue.data.length === 0) return null;

	return (
		<div className="card bg-base-100 shadow-lg">
			<div className="card-body p-4">
				<h3 className="text-xs font-bold uppercase tracking-wider text-base-content/50 mb-3 flex items-center gap-2">
					<History className="h-3 w-3" />
					Recent Successes
				</h3>
				<div className="space-y-2">
					{queue.data.map((item) => (
						<div key={item.id} className="flex items-center justify-between gap-4 text-sm">
							<div className="flex items-center gap-2 truncate min-w-0">
								<CheckCircle2 className="h-3.5 w-3.5 text-success shrink-0" />
								<span className="truncate" title={item.nzb_path}>
									{item.target_path || item.nzb_path.split('/').pop()}
								</span>
							</div>
							<span className="text-[10px] text-base-content/40 whitespace-nowrap">
								{formatRelativeTime(item.updated_at)}
							</span>
						</div>
					))}
				</div>
			</div>
		</div>
	);
}
