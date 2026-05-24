import { useState } from "react";
import { ShieldAlert, Trash2, CheckCircle, XCircle, RefreshCw, BarChart2 } from "lucide-react";
import { useIndexerStats, useCleanupIndexerStats } from "../../../hooks/useApi";
import { useToast } from "../../../contexts/ToastContext";
import { useConfirm } from "../../../contexts/ModalContext";

export function IndexerHealth() {
	const { data: stats, isLoading, error, refetch } = useIndexerStats();
	const cleanupStats = useCleanupIndexerStats();
	const { showToast } = useToast();
	const { confirmAction } = useConfirm();
	
	const [showPruneModal, setShowPruneModal] = useState(false);
	const [pruneOption, setPruneOption] = useState<"24h" | "7d" | "30d" | "custom">("24h");
	const [customDays, setCustomDays] = useState(3);

	const handlePrune = async () => {
		let hours = 24;
		let label = "Last 24 Hours";
		
		if (pruneOption === "7d") {
			hours = 7 * 24;
			label = "Last 7 Days";
		} else if (pruneOption === "30d") {
			hours = 30 * 24;
			label = "Last 30 Days";
		} else if (pruneOption === "custom") {
			if (customDays <= 0) {
				showToast({
					title: "Invalid Input",
					message: "Please enter a positive number of days",
					type: "error",
				});
				return;
			}
			hours = customDays * 24;
			label = `Last ${customDays} Days`;
		}

		const confirmed = await confirmAction(
			"Prune Indexer Statistics",
			`Are you sure you want to delete all logged indexer statistics from the ${label}? This cannot be undone.`,
			{
				type: "warning",
				confirmText: "Prune Data",
				confirmButtonClass: "btn-warning",
			}
		);

		if (!confirmed) return;

		try {
			const res = await cleanupStats.mutateAsync({ hours });
			showToast({
				title: "Stats Pruned",
				message: `Successfully pruned ${res.pruned_rows} statistics records.`,
				type: "success",
			});
			setShowPruneModal(false);
			void refetch();
		} catch (err) {
			console.error("Failed to prune indexer stats:", err);
			showToast({
				title: "Pruning Failed",
				message: "An error occurred while pruning indexer statistics.",
				type: "error",
			});
		}
	};

	if (isLoading) {
		return (
			<div className="flex h-64 items-center justify-center">
				<div className="flex flex-col items-center space-y-4">
					<RefreshCw className="h-8 w-8 animate-spin text-primary" />
					<p className="text-base-content/60 text-sm">Loading indexer statistics...</p>
				</div>
			</div>
		);
	}

	if (error) {
		return (
			<div className="alert alert-error shadow-lg">
				<ShieldAlert className="h-6 w-6 shrink-0" />
				<div>
					<h3 className="font-bold">Error Loading Statistics</h3>
					<div className="text-xs">Failed to load persistent indexer import history.</div>
				</div>
			</div>
		);
	}

	const hasStats = stats && stats.length > 0;

	return (
		<div className="space-y-6">
			{/* Top Header Card */}
			<div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
				<div>
					<h3 className="font-bold text-lg">Usenet Indexers Health</h3>
					<p className="text-base-content/60 text-xs sm:text-sm">
						Persistent lookup success and failure rates extracted from webhook and NZB metadata.
					</p>
				</div>
				<div className="flex items-center gap-2">
					<button
						className="btn btn-ghost btn-sm border-2 border-base-300/50"
						onClick={() => void refetch()}
					>
						<RefreshCw className="h-4 w-4" />
						Refresh
					</button>
					<button
						className="btn btn-warning btn-sm gap-2"
						onClick={() => setShowPruneModal(true)}
						disabled={!hasStats}
					>
						<Trash2 className="h-4 w-4" />
						Prune Statistics
					</button>
				</div>
			</div>

			{/* Indexers Stats List */}
			{!hasStats ? (
				<div className="hero rounded-2xl border-2 border-dashed border-base-300/50 bg-base-100/50 py-16">
					<div className="hero-content text-center">
						<div className="max-w-md space-y-4">
							<div className="mx-auto flex h-16 w-16 items-center justify-center rounded-2xl bg-base-300/50 text-base-content/40">
								<BarChart2 className="h-8 w-8" />
							</div>
							<h3 className="font-bold text-xl">No Indexer History Yet</h3>
							<p className="text-base-content/60 text-sm">
								Indexer statistics will populate automatically as active imports finalize in the queue.
							</p>
						</div>
					</div>
				</div>
			) : (
				<div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
					{stats.map((item) => {
						const isHealthy = item.success_rate >= 85;
						const isMedium = item.success_rate >= 60 && item.success_rate < 85;
						
						let progressColor = "bg-error";
						let badgeColor = "badge-error";
						let textColor = "text-error";
						if (isHealthy) {
							progressColor = "bg-success";
							badgeColor = "badge-success";
							textColor = "text-success";
						} else if (isMedium) {
							progressColor = "bg-warning";
							badgeColor = "badge-warning";
							textColor = "text-warning";
						}

						return (
							<div
								key={item.indexer}
								className="card relative overflow-hidden border-2 border-base-300/30 bg-base-100/60 shadow-lg backdrop-blur-md transition-all duration-300 hover:-translate-y-1 hover:border-base-300/80 hover:shadow-xl"
							>
								{/* Glassmorphic top indicator line */}
								<div className={`absolute top-0 left-0 right-0 h-1.5 ${progressColor}`} />
								
								<div className="card-body p-6">
									<div className="flex items-start justify-between">
										<div>
											<h4 className="font-bold text-base-content text-lg tracking-tight">
												{item.indexer}
											</h4>
											<span className="text-base-content/40 text-xs">Originating Indexer</span>
										</div>
										<div className="flex items-center gap-2">
											<button
												className="btn btn-ghost btn-xs text-error hover:bg-error/10"
												onClick={async () => {
													const confirmed = await confirmAction(
														"Delete Indexer Stats",
														`Are you sure you want to delete all statistics for "${item.indexer}"?`,
														{
															type: "warning",
															confirmText: "Delete",
															confirmButtonClass: "btn-error",
														}
													);
													if (confirmed) {
														try {
															// Directly call cleanup with indexer param
															await cleanupStats.mutateAsync({ indexer: item.indexer });
															showToast({
																title: "Indexer Stats Deleted",
																message: `Successfully deleted statistics for ${item.indexer}.`,
																type: "success",
															});
															void refetch();
														} catch (err) {
															showToast({
																title: "Delete Failed",
																message: `Failed to delete stats for ${item.indexer}.`,
																type: "error",
															});
														}
													}
												}}
											>
												<Trash2 className="h-4 w-4" />
											</button>
											<div className={`badge ${badgeColor} badge-sm font-semibold uppercase tracking-wider p-2.5`}>
												{item.success_rate.toFixed(1)}% Health
											</div>
										</div>
									</div>

									{/* Health Ratio Bar */}
									<div className="mt-4 space-y-1.5">
										<div className="flex justify-between text-xs">
											<span className="font-medium text-base-content/60">Success Ratio</span>
											<span className={`font-semibold ${textColor}`}>
												{item.success_count} / {item.total_imports}
											</span>
										</div>
										<div className="relative h-2 w-full overflow-hidden rounded-full bg-base-300/70">
											<div
												className={`h-full rounded-full transition-all duration-500 ${progressColor}`}
												style={{ width: `${item.success_rate}%` }}
											/>
										</div>
									</div>

									{/* Numeric Stats Grid */}
									<div className="mt-6 grid grid-cols-3 gap-2 rounded-xl bg-base-300/25 p-3 text-center border border-base-300/20">
										<div>
											<div className="font-bold text-base-content text-sm sm:text-base">{item.total_imports}</div>
											<div className="text-[10px] text-base-content/40 font-medium uppercase tracking-wider">Total</div>
										</div>
										<div>
											<div className="font-bold text-success text-sm sm:text-base flex items-center justify-center gap-1">
												<CheckCircle className="h-3 w-3" />
												{item.success_count}
											</div>
											<div className="text-[10px] text-base-content/40 font-medium uppercase tracking-wider">Healthy</div>
										</div>
										<div>
											<div className="font-bold text-error text-sm sm:text-base flex items-center justify-center gap-1">
												<XCircle className="h-3 w-3" />
												{item.failed_count}
											</div>
											<div className="text-[10px] text-base-content/40 font-medium uppercase tracking-wider">Failed</div>
										</div>
									</div>
								</div>
							</div>
						);
					})}
				</div>
			)}

			{/* Prune Statistics Modal */}
			{showPruneModal && (
				<div className="modal modal-open backdrop-blur-sm">
					<div className="modal-box border-2 border-base-300/50 bg-base-100 shadow-2xl p-6 sm:p-8">
						<h3 className="font-bold text-xl flex items-center gap-2">
							<Trash2 className="h-6 w-6 text-warning" />
							Prune Statistics
						</h3>
						<p className="py-4 text-base-content/60 text-sm">
							Choose the time period of historical statistics you would like to clear.
						</p>

						<div className="space-y-4">
							<div className="form-control">
								<label className="label cursor-pointer justify-start gap-3 rounded-xl border-2 border-base-300/40 p-4 transition hover:bg-base-200/50">
									<input
										type="radio"
										name="prune_option"
										className="radio radio-primary"
										checked={pruneOption === "24h"}
										onChange={() => setPruneOption("24h")}
									/>
									<div>
										<span className="font-bold text-sm">Delete Last 24 Hours</span>
										<p className="text-base-content/50 text-[10px] sm:text-xs">Resets statistics from the most recent day only.</p>
									</div>
								</label>
							</div>

							<div className="form-control">
								<label className="label cursor-pointer justify-start gap-3 rounded-xl border-2 border-base-300/40 p-4 transition hover:bg-base-200/50">
									<input
										type="radio"
										name="prune_option"
										className="radio radio-primary"
										checked={pruneOption === "7d"}
										onChange={() => setPruneOption("7d")}
									/>
									<div>
										<span className="font-bold text-sm">Delete Last 7 Days</span>
										<p className="text-base-content/50 text-[10px] sm:text-xs">Resets the last week of collected indexer data.</p>
									</div>
								</label>
							</div>

							<div className="form-control">
								<label className="label cursor-pointer justify-start gap-3 rounded-xl border-2 border-base-300/40 p-4 transition hover:bg-base-200/50">
									<input
										type="radio"
										name="prune_option"
										className="radio radio-primary"
										checked={pruneOption === "30d"}
										onChange={() => setPruneOption("30d")}
									/>
									<div>
										<span className="font-bold text-sm">Delete Last 30 Days</span>
										<p className="text-base-content/50 text-[10px] sm:text-xs">Clears the past month of statistics.</p>
									</div>
								</label>
							</div>

							<div className="form-control">
								<label className="label cursor-pointer justify-start gap-3 rounded-xl border-2 border-base-300/40 p-4 transition hover:bg-base-200/50">
									<input
										type="radio"
										name="prune_option"
										className="radio radio-primary"
										checked={pruneOption === "custom"}
										onChange={() => setPruneOption("custom")}
									/>
									<div className="flex-1">
										<span className="font-bold text-sm">Delete Custom Period</span>
										<p className="text-base-content/50 text-[10px] sm:text-xs">Specify a custom number of days to clear.</p>
										{pruneOption === "custom" && (
											<div className="mt-3 flex items-center gap-3">
												<input
													type="number"
													className="input input-bordered input-sm w-24 text-center font-bold"
													value={customDays}
													onChange={(e) => setCustomDays(parseInt(e.target.value) || 0)}
													min="1"
												/>
												<span className="text-xs text-base-content/60 font-semibold">Days of data</span>
											</div>
										)}
									</div>
								</label>
							</div>
						</div>

						<div className="modal-action mt-6 gap-2">
							<button
								className="btn btn-ghost"
								onClick={() => setShowPruneModal(false)}
								disabled={cleanupStats.isPending}
							>
								Cancel
							</button>
							<button
								className="btn btn-warning gap-2"
								onClick={handlePrune}
								disabled={cleanupStats.isPending}
							>
								{cleanupStats.isPending && <RefreshCw className="h-4 w-4 animate-spin" />}
								Prune Statistics
							</button>
						</div>
					</div>
				</div>
			)}
		</div>
	);
}
