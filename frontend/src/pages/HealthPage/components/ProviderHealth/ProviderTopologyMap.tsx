import { AlertCircle, Cpu, Globe, Server, ShieldCheck, Target, Terminal } from "lucide-react";
import { useMemo, useState } from "react";
import { formatBytes, getProviderBrandName } from "../../../../lib/utils";
import type { ProviderStatus } from "../../../../types/api";

// Calculate health score similar to calculateHealthScore helper
const getScore = (provider: ProviderStatus) => {
	let score = 100;
	if (provider.state !== "connected" && provider.state !== "active") return 0;
	if (provider.ping_ms > 1000) score -= 40;
	else if (provider.ping_ms > 500) score -= 25;
	else if (provider.ping_ms > 200) score -= 10;
	else if (provider.ping_ms > 100) score -= 5;
	score -= Math.min(30, provider.error_count * 5);
	if (provider.missing_warning) score -= 20;
	if (provider.missing_count > 5000) score -= 15;
	else if (provider.missing_count > 1000) score -= 10;
	return Math.max(0, score);
};

interface ProviderTopologyMapProps {
	providers: ProviderStatus[];
	totalSpeed: number;
}

export function ProviderTopologyMap({ providers, totalSpeed }: ProviderTopologyMapProps) {
	const count = providers.length;
	const [selectedId, setSelectedId] = useState<string | null>(null);
	const [hoveredId, setHoveredId] = useState<string | null>(null);

	// Determine active targeted node (hovered takes priority over selected)
	const activeTargetId = hoveredId || selectedId;

	const nodes = useMemo(() => {
		const cx = 310; // Shifted left to make room for the telemetry panel
		const cy = 150; // Center Y
		const rx = 175; // Width Radius
		const ry = 95; // Height Radius (ellipse)

		return providers.map((provider, i) => {
			const angle = count > 1 ? i * ((2 * Math.PI) / count) - Math.PI / 2 : 0;
			const x = cx + rx * Math.cos(angle);
			const y = cy + ry * Math.sin(angle);
			const score = getScore(provider);

			let statusColor = "stroke-rose-500 fill-rose-500/10";
			let glowId = "glow-rose";
			let textColor = "text-rose-400";
			let badgeBg = "bg-rose-500/10 border-rose-500/20 text-rose-400";

			if (score >= 85) {
				statusColor = "stroke-emerald-400 fill-emerald-400/10";
				glowId = "glow-emerald";
				textColor = "text-emerald-400";
				badgeBg = "bg-emerald-500/10 border-emerald-500/20 text-emerald-400";
			} else if (score >= 50) {
				statusColor = "stroke-amber-400 fill-amber-400/10";
				glowId = "glow-amber";
				textColor = "text-amber-400";
				badgeBg = "bg-amber-500/10 border-amber-500/20 text-amber-400";
			}

			// Speed-scaled flowing data packet calculations
			const speedBytes = provider.current_speed_bytes_per_sec || 0;
			let animDuration = "0s";
			let packetCount = 0;
			let packetColor = "fill-cyan-400";
			let packetGlow = "drop-shadow-[0_0_5px_#22d3ee]";

			if (speedBytes > 0) {
				packetCount = 2;
				animDuration = "3.2s";
				if (speedBytes > 10 * 1024 * 1024) {
					// >10MB/s
					packetCount = 3;
					animDuration = "1.8s";
				}
				if (speedBytes > 50 * 1024 * 1024) {
					// >50MB/s
					packetCount = 4;
					animDuration = "0.85s";
					packetColor = "fill-cyan-300";
					packetGlow = "drop-shadow-[0_0_8px_#22d3ee]";
				}
			}

			return {
				provider,
				x,
				y,
				score,
				statusColor,
				glowId,
				textColor,
				badgeBg,
				animDuration,
				packetCount,
				packetColor,
				packetGlow,
			};
		});
	}, [providers, count]);

	// Extract details for the targeted HUD panel
	const targetedNode = useMemo(() => {
		if (!activeTargetId) return null;
		return nodes.find((n) => n.provider.id === activeTargetId);
	}, [nodes, activeTargetId]);

	const totalActiveConnections = useMemo(() => {
		return providers.reduce((sum, p) => sum + (p.used_connections || 0), 0);
	}, [providers]);

	return (
		<div className="card overflow-hidden border border-base-200/40 bg-base-100/20 shadow-xl backdrop-blur-md">
			{/* CSS styling embedded directly inside the SVG defs */}
			<div className="flex items-center justify-between border-base-200/50 border-b p-4">
				<div>
					<h3 className="flex items-center gap-2 font-bold text-base text-base-content/90">
						<Globe className="h-4 w-4 animate-pulse text-primary" />
						Live Connection Constellation
					</h3>
					<p className="text-[11px] text-base-content/50">
						Active route pathways, speed-scaled laser-bead streams, and locked targeting diagnostics
					</p>
				</div>
				<div className="flex items-center gap-3 text-xs">
					{selectedId && (
						<button
							type="button"
							onClick={() => setSelectedId(null)}
							className="rounded border border-primary/20 bg-primary/10 px-2 py-0.5 font-mono text-[10px] text-primary transition-all hover:bg-primary/20"
						>
							Clear Target
						</button>
					)}
					<span className="flex items-center gap-1.5 rounded border border-emerald-500/20 bg-emerald-500/10 px-2 py-0.5 font-mono text-[10px] text-emerald-400">
						<span className="h-1.5 w-1.5 animate-ping rounded-full bg-emerald-400" />
						Active:{" "}
						{providers.filter((p) => p.state === "connected" || p.state === "active").length}
					</span>
					<span className="rounded border border-primary/20 bg-primary/10 px-2 py-0.5 font-mono text-[11px] text-primary">
						Total Speed: {formatBytes(totalSpeed)}/s
					</span>
				</div>
			</div>

			<div className="relative flex min-h-[340px] w-full select-none items-center justify-center overflow-hidden bg-black/40 py-4">
				<svg
					className="h-[310px] w-full max-w-[800px]"
					viewBox="0 0 800 310"
					xmlns="http://www.w3.org/2000/svg"
				>
					<title>Live Connection Constellation</title>
					<defs>
						<style>{`
							@keyframes rotate-clockwise {
								from { transform: rotate(0deg); }
								to { transform: rotate(360deg); }
							}
							@keyframes rotate-counter {
								from { transform: rotate(0deg); }
								to { transform: rotate(-360deg); }
							}
							@keyframes radar-sweep {
								from { transform: rotate(0deg); }
								to { transform: rotate(360deg); }
							}
							@keyframes target-pulse {
								0%, 100% { transform: scale(1); opacity: 0.9; }
								50% { transform: scale(1.06); opacity: 0.45; }
							}
							@keyframes satellite-orbit {
								from { transform: rotate(0deg); }
								to { transform: rotate(360deg); }
							}
							@keyframes grid-glow {
								0%, 100% { opacity: 0.15; }
								50% { opacity: 0.25; }
							}
							@keyframes sweep-line {
								0% { transform: rotate(0deg); }
								100% { transform: rotate(360deg); }
							}
							@keyframes tech-pulse {
								0%, 100% { opacity: 0.3; }
								50% { opacity: 0.85; }
							}
							@keyframes wave-h-shift {
								0% { transform: translateX(0); }
								100% { transform: translateX(-40px); }
							}
							.spin-cw {
								animation: rotate-clockwise 20s linear infinite;
							}
							.spin-ccw {
								animation: rotate-counter 12s linear infinite;
							}
							.orbit-element {
								animation: satellite-orbit 6s linear infinite;
							}
							.pulse-slow {
								animation: tech-pulse 3s ease-in-out infinite;
							}
						`}</style>

						{/* Tactical Grid Pattern */}
						<pattern id="hud-grid" width="25" height="25" patternUnits="userSpaceOnUse">
							<path d="M 25 0 L 0 0 0 25" fill="none" stroke="rgba(59, 130, 246, 0.025)" strokeWidth="1" />
							<circle cx="0" cy="0" r="1.2" fill="rgba(59, 130, 246, 0.1)" />
						</pattern>

						{/* Radar Sonar Sweep Radial Gradient */}
						<linearGradient id="radar-sweep-gradient" x1="310" y1="150" x2="610" y2="150" gradientUnits="userSpaceOnUse">
							<stop offset="0%" stopColor="#3b82f6" stopOpacity="0.25" />
							<stop offset="25%" stopColor="#22d3ee" stopOpacity="0.08" />
							<stop offset="100%" stopColor="#3b82f6" stopOpacity="0" />
						</linearGradient>

						{/* Glow Filters */}
						<filter id="glow-emerald" x="-40%" y="-40%" width="180%" height="180%">
							<feGaussianBlur stdDeviation="5" result="blur" />
							<feMerge>
								<feMergeNode in="blur" />
								<feMergeNode in="SourceGraphic" />
							</feMerge>
						</filter>
						<filter id="glow-amber" x="-40%" y="-40%" width="180%" height="180%">
							<feGaussianBlur stdDeviation="5" result="blur" />
							<feMerge>
								<feMergeNode in="blur" />
								<feMergeNode in="SourceGraphic" />
							</feMerge>
						</filter>
						<filter id="glow-rose" x="-40%" y="-40%" width="180%" height="180%">
							<feGaussianBlur stdDeviation="5" result="blur" />
							<feMerge>
								<feMergeNode in="blur" />
								<feMergeNode in="SourceGraphic" />
							</feMerge>
						</filter>
						<filter id="glow-primary" x="-40%" y="-40%" width="180%" height="180%">
							<feGaussianBlur stdDeviation="8" result="blur" />
							<feMerge>
								<feMergeNode in="blur" />
								<feMergeNode in="SourceGraphic" />
							</feMerge>
						</filter>

						{/* Core to Node Color Gradients */}
						{nodes.map((node) => (
							<linearGradient
								key={`grad-${node.provider.id}`}
								id={`grad-${node.provider.id}`}
								x1="310"
								y1="150"
								x2={node.x}
								y2={node.y}
								gradientUnits="userSpaceOnUse"
							>
								<stop offset="0%" stopColor="#3b82f6" stopOpacity="0.75" />
								<stop
									offset="100%"
									stopColor={
										node.score >= 85 ? "#34d399" : node.score >= 50 ? "#fbbf24" : "#f43f5e"
									}
									stopOpacity="0.9"
								/>
							</linearGradient>
						))}
					</defs>

					{/* 1. Tactical Grid Backdrop */}
					<rect width="800" height="310" fill="url(#hud-grid)" rx="8" />

					{/* HUD Outer L-Brackets */}
					<path d="M 15 35 L 15 15 L 35 15" stroke="rgba(59, 130, 246, 0.4)" strokeWidth="1.5" fill="none" />
					<path d="M 785 35 L 785 15 L 765 15" stroke="rgba(59, 130, 246, 0.4)" strokeWidth="1.5" fill="none" />
					<path d="M 15 275 L 15 295 L 35 295" stroke="rgba(59, 130, 246, 0.4)" strokeWidth="1.5" fill="none" />
					<path d="M 785 275 L 785 295 L 765 295" stroke="rgba(59, 130, 246, 0.4)" strokeWidth="1.5" fill="none" />

					{/* Cyber Labels */}
					<text x="45" y="25" className="fill-blue-500/50 font-bold font-mono text-[8px] uppercase tracking-wider">
						Telemetry: Sweep Active
					</text>
					<text x="45" y="295" className="fill-blue-500/40 font-mono text-[8px] tracking-wider">
						LOC_COORD: 39°44'N 104°59'W // [SYS_SECURE]
					</text>

					{/* 2. Concentric Target Rings */}
					<ellipse cx="310" cy="150" rx="220" ry="115" className="fill-none stroke-white/5 stroke-[0.8]" strokeDasharray="3, 12" />
					<ellipse cx="310" cy="150" rx="175" ry="95" className="fill-none stroke-blue-500/10 stroke-[1.2]" strokeDasharray="6, 8" />
					<ellipse cx="310" cy="150" rx="110" ry="60" className="fill-none stroke-white/5 stroke-[0.8]" />

					{/* 3. Concentric Rotating Radar Sweep Line */}
					<line
						x1="310"
						y1="150"
						x2="610"
						y2="150"
						stroke="url(#radar-sweep-gradient)"
						strokeWidth="2.5"
						style={{
							transformOrigin: "310px 150px",
							animation: "radar-sweep 7s linear infinite",
						}}
					/>

					{/* 4. Connection Pipelines & Flows */}
					{nodes.map((node) => {
						const isStreaming = node.animDuration !== "0s";
						const isTargeted = activeTargetId === node.provider.id;
						const dimOpacity = activeTargetId && !isTargeted ? "opacity-10" : "opacity-100";

						return (
							<g key={node.provider.id} className={`transition-opacity duration-300 ${dimOpacity}`}>
								{/* Thick energy tube backdrop */}
								<line
									x1="310"
									y1="150"
									x2={node.x}
									y2={node.y}
									stroke={`url(#grad-${node.provider.id})`}
									className={`stroke-[3.5] fill-none transition-all duration-300 ${
										isTargeted ? "opacity-35 stroke-[4.5]" : "opacity-10"
									}`}
								/>

								{/* Primary structural vector path */}
								<line
									x1="310"
									y1="150"
									x2={node.x}
									y2={node.y}
									stroke={`url(#grad-${node.provider.id})`}
									className={`stroke-[1.2] fill-none transition-all duration-300 ${
										isStreaming ? "opacity-80" : "opacity-30"
									}`}
									strokeDasharray={isTargeted ? "none" : "5, 5"}
								/>

								{/* Marching Laser-Bead Particles (Speed Scaled) */}
								{isStreaming && node.packetCount > 0 && (
									<>
										{/* Packet 1 */}
										<circle r="4" className={`${node.packetColor} ${node.packetGlow}`}>
											<animate attributeName="cx" from="310" to={node.x} dur={node.animDuration} repeatCount="indefinite" />
											<animate attributeName="cy" from="150" to={node.y} dur={node.animDuration} repeatCount="indefinite" />
										</circle>

										{/* Packet 2 */}
										{node.packetCount >= 2 && (
											<circle r="3.2" className={`${node.packetColor} ${node.packetGlow} opacity-80`}>
												<animate
													attributeName="cx"
													from="310"
													to={node.x}
													dur={node.animDuration}
													begin={`${parseFloat(node.animDuration) * 0.35}s`}
													repeatCount="indefinite"
												/>
												<animate
													attributeName="cy"
													from="150"
													to={node.y}
													dur={node.animDuration}
													begin={`${parseFloat(node.animDuration) * 0.35}s`}
													repeatCount="indefinite"
												/>
											</circle>
										)}

										{/* Packet 3 */}
										{node.packetCount >= 3 && (
											<circle r="2.5" className={`${node.packetColor} ${node.packetGlow} opacity-60`}>
												<animate
													attributeName="cx"
													from="310"
													to={node.x}
													dur={node.animDuration}
													begin={`${parseFloat(node.animDuration) * 0.65}s`}
													repeatCount="indefinite"
												/>
												<animate
													attributeName="cy"
													from="150"
													to={node.y}
													dur={node.animDuration}
													begin={`${parseFloat(node.animDuration) * 0.65}s`}
													repeatCount="indefinite"
												/>
											</circle>
										)}

										{/* Packet 4 */}
										{node.packetCount >= 4 && (
											<circle r="2" className={`${node.packetColor} ${node.packetGlow} opacity-40`}>
												<animate
													attributeName="cx"
													from="310"
													to={node.x}
													dur={node.animDuration}
													begin={`${parseFloat(node.animDuration) * 0.82}s`}
													repeatCount="indefinite"
												/>
												<animate
													attributeName="cy"
													from="150"
													to={node.y}
													dur={node.animDuration}
													begin={`${parseFloat(node.animDuration) * 0.82}s`}
													repeatCount="indefinite"
												/>
											</circle>
										)}
									</>
								)}
							</g>
						);
					})}

					{/* 5. Central Reactor Core Node */}
					<g className="cursor-pointer" transform="translate(0, 0)">
						<circle
							cx="310"
							cy="150"
							r="42"
							className="fill-none stroke-blue-500/10 stroke-[1]"
						/>
						{/* Outer Rotating Gear (Clockwise) */}
						<circle
							cx="310"
							cy="150"
							r="38"
							className="spin-cw fill-none stroke-primary/30 stroke-[1.8]"
							strokeDasharray="6, 12"
							style={{ transformOrigin: "310px 150px" }}
						/>
						{/* Inner Rotating Gear (Counter-Clockwise) */}
						<circle
							cx="310"
							cy="150"
							r="30"
							className="spin-ccw fill-none stroke-cyan-400/40 stroke-[1.2]"
							strokeDasharray="4, 6"
							style={{ transformOrigin: "310px 150px" }}
						/>
						{/* Central Glow Orb */}
						<circle
							cx="310"
							cy="150"
							r="23"
							className="fill-neutral-950 stroke-[2.5] stroke-primary/75"
							filter="url(#glow-primary)"
						/>
						<circle
							cx="310"
							cy="150"
							r="18"
							className="fill-primary/10 stroke-none"
						/>
						<g transform="translate(299, 139)" className="pointer-events-none text-primary pulse-slow">
							<Cpu className="h-[22px] w-[22px] stroke-[1.5] stroke-primary" />
						</g>
						{/* Reactor Coordinates & Tags */}
						<text
							x="310"
							y="204"
							textAnchor="middle"
							className="fill-primary/80 font-bold font-mono text-[8px] uppercase tracking-widest"
						>
							ALTMOUNT CORE
						</text>
					</g>

					{/* 6. Orbiting Provider Nodes */}
					{nodes.map((node) => {
						const isTargeted = activeTargetId === node.provider.id;
						const isSelected = selectedId === node.provider.id;
						const dimOpacity = activeTargetId && !isTargeted ? "opacity-15" : "opacity-100";

						return (
							<g
								key={node.provider.id}
								className={`group cursor-pointer transition-all duration-300 ${dimOpacity}`}
								transform={`translate(${node.x}, ${node.y})`}
								onMouseEnter={() => setHoveredId(node.provider.id)}
								onMouseLeave={() => setHoveredId(null)}
								onClick={() => setSelectedId(isSelected ? null : node.provider.id)}
							>
								{/* Pulse Rings */}
								<circle
									r="19"
									className={`fill-none stroke-[1.5] transition-all ${
										node.score >= 85
											? "stroke-emerald-500/50"
											: node.score >= 50
												? "stroke-amber-500/50"
												: "stroke-rose-500/50"
									}`}
									style={{
										animation: "target-pulse 2.2s cubic-bezier(0.16, 1, 0.3, 1) infinite",
										transformOrigin: "0px 0px",
									}}
								/>

								{/* Orbit path and tiny orbiting diagnostic satellite */}
								{node.provider.state === "connected" && (
									<>
										<circle r="23" className="fill-none stroke-blue-500/10 stroke-[0.5] stroke-dasharray-[2, 4]" />
										<g className="orbit-element" style={{ transformOrigin: "0px 0px" }}>
											<circle cx="23" cy="0" r="2.2" className="fill-cyan-400 filter drop-shadow-[0_0_3px_#22d3ee]" />
										</g>
									</>
								)}

								{/* Target locked pulsing brackets around node */}
								{isTargeted && (
									<g className="scale-105 transition-transform duration-300">
										{/* Pulsing selection ring */}
										<rect
											x="-24"
											y="-24"
											width="48"
											height="48"
											rx="3"
											className="fill-none stroke-cyan-400 stroke-[1.5]"
											strokeDasharray="8, 16"
											style={{
												animation: "target-pulse 1.5s ease-in-out infinite",
												transformOrigin: "0px 0px",
											}}
										/>
										{/* Corner indicators */}
										<path d="M -27 -17 L -27 -27 L -17 -27" stroke="#22d3ee" strokeWidth="2" fill="none" />
										<path d="M 27 -17 L 27 -27 L 17 -27" stroke="#22d3ee" strokeWidth="2" fill="none" />
										<path d="M -27 17 L -27 27 L -17 27" stroke="#22d3ee" strokeWidth="2" fill="none" />
										<path d="M 27 17 L 27 27 L 17 27" stroke="#22d3ee" strokeWidth="2" fill="none" />
									</g>
								)}

								{/* Node Outer Circle */}
								<circle
									r="16"
									className={`stroke-[2] transition-colors ${node.statusColor}`}
									filter={`url(#${node.glowId})`}
								/>
								{/* Node Inner Circle */}
								<circle
									r="12"
									className="fill-neutral-950 stroke-[1] stroke-white/5"
								/>

								{/* Core Server Icon */}
								<g transform="translate(-8, -8)" className="pointer-events-none text-base-content/85">
									<Server className="h-4 w-4 stroke-[1.5] stroke-current" />
								</g>

								{/* Node Text Label & Stats Panel */}
								<g transform="translate(0, 24)">
									{/* Brand Name */}
									<text
										x="0"
										y="0"
										textAnchor="middle"
										className="fill-base-content/90 font-bold font-mono text-[9px] tracking-wide"
									>
										{getProviderBrandName(node.provider.host)}
									</text>

									{/* Inline Latency Metric Indicator */}
									<g transform="translate(0, 11)">
										<circle
											cx="-16"
											cy="-3"
											r="2.5"
											className={
												node.score >= 85
													? "fill-emerald-400"
													: node.score >= 50
														? "fill-amber-400"
														: "fill-rose-500"
											}
										/>
										<text
											x="-8"
											y="0"
											className="fill-base-content/50 font-semibold font-mono text-[8px]"
										>
											{node.provider.ping_ms > 0 ? `${node.provider.ping_ms}ms` : "down"}
										</text>
									</g>

									{/* Micro Connection Thread Meter (Segmented row) */}
									<g transform="translate(-18, 18)">
										{[0, 1, 2, 3, 4].map((idx) => {
											const maxC = node.provider.max_connections || 1;
											const ratio = (node.provider.used_connections || 0) / maxC;
											const activeIdx = Math.ceil(ratio * 5);
											const isThreadActive = idx < activeIdx && node.provider.used_connections > 0;

											return (
												<rect
													key={`thread-${node.provider.id}-${idx}`}
													x={idx * 8}
													y="0"
													width="5"
													height="2.5"
													rx="0.5"
													className={`transition-colors ${
														isThreadActive
															? "fill-cyan-400 shadow-[0_0_2px_rgba(34,211,238,0.7)]"
															: "fill-white/10"
													}`}
												/>
											);
										})}
									</g>
								</g>
							</g>
						);
					})}

					{/* 7. Tactical Sidebar Diagnostics HUD Panel */}
					<g transform="translate(0, 0)">
						{/* HUD border enclosure */}
						<rect
							x="545"
							y="20"
							width="235"
							height="265"
							fill="rgba(5, 7, 15, 0.88)"
							stroke="rgba(59, 130, 246, 0.3)"
							strokeWidth="1.2"
							rx="6"
							className="backdrop-blur-sm"
						/>
						{/* Sub-Header bar */}
						<line x1="545" y1="46" x2="780" y2="46" stroke="rgba(59, 130, 246, 0.3)" strokeWidth="1" />
						<line x1="545" y1="245" x2="780" y2="245" stroke="rgba(59, 130, 246, 0.3)" strokeWidth="0.8" strokeDasharray="3, 3" />

						{/* Header content */}
						<g transform="translate(557, 34)">
							<Target className={`h-3.5 w-3.5 ${targetedNode ? "text-cyan-400 animate-pulse" : "text-blue-500/50"}`} />
							<text x="18" y="10" className="fill-cyan-400 font-bold font-mono text-[9px] uppercase tracking-wider">
								{targetedNode ? "TARGET_LOCK_ESTABLISHED" : "SYSTEM_SWEEP_MONITOR"}
							</text>
						</g>

						{/* Interactive HUD Readouts */}
						{!targetedNode ? (
							/* CORE SYSTEM READOUT (Fallback state) */
							<g transform="translate(557, 62)">
								{/* Tech details */}
								<g transform="translate(0, 0)">
									<text x="0" y="10" className="fill-slate-400 font-bold font-mono text-[8px] uppercase tracking-wider">
										SYSTEM:
									</text>
									<text x="75" y="10" className="fill-emerald-400 font-bold font-mono text-[8.5px]">
										ONLINE // ACTIVE
									</text>

									<text x="0" y="24" className="fill-slate-400 font-mono text-[8px]">
										ROUTERS:
									</text>
									<text x="75" y="24" className="fill-slate-200 font-mono text-[8px]">
										{nodes.length} CONFIG SEEDERS
									</text>

									<text x="0" y="38" className="fill-slate-400 font-mono text-[8px]">
										ACTIVE POOL:
									</text>
									<text x="75" y="38" className="fill-cyan-400 font-bold font-mono text-[8px]">
										{totalActiveConnections} THREADS
									</text>

									<text x="0" y="52" className="fill-slate-400 font-mono text-[8px]">
										ENCRYPTION:
									</text>
									<text x="75" y="52" className="fill-emerald-500/80 font-semibold font-mono text-[8px]">
										TLSv1.3 ACCEL
									</text>
								</g>

								{/* Animated signal visualizer bar chart */}
								<g transform="translate(0, 68)">
									<text x="0" y="10" className="fill-slate-400 font-bold font-mono text-[8px] uppercase tracking-wider">
										CORE SPEED TELEMETRY:
									</text>
									<text x="0" y="24" className="fill-cyan-400 font-bold font-mono text-[13px]">
										{formatBytes(totalSpeed)}/s
									</text>

									{/* Simulated Audio/Wave frequency bars */}
									<g transform="translate(0, 36)">
										{[0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19].map((idx) => {
											// Seed a shifting wave heights
											const hVal = 4 + Math.sin(idx * 0.6 + (totalSpeed > 0 ? Date.now() / 250 : 0)) * 6 + 7;
											return (
												<rect
													key={`wave-${idx}`}
													x={idx * 11}
													y={25 - hVal}
													width="4.5"
													height={hVal}
													className="fill-blue-500/20 stroke-none"
												/>
											);
										})}
									</g>
								</g>

								{/* Instruct user */}
								<g transform="translate(0, 138)">
									<rect x="0" y="0" width="211" height="34" rx="3" className="fill-blue-950/20 stroke-[0.8] stroke-blue-500/10" />
									<Terminal className="absolute x-2 y-2 h-3.5 w-3.5 text-blue-400/60" />
									<text x="24" y="15" className="fill-blue-400 font-medium font-mono text-[7.5px]">
										SELECT A NODE FOR SECURE
									</text>
									<text x="24" y="25" className="fill-blue-400 font-medium font-mono text-[7.5px]">
										DECRYPTED DEEP LINK DETAILS
									</text>
								</g>
							</g>
						) : (
							/* TARGET SPECIFIC NODE DIAGNOSTICS (Target Lock State) */
							<g transform="translate(557, 60)">
								{/* Node Host Header */}
								<g transform="translate(0, 4)">
									<text x="0" y="8" className="fill-slate-400 font-mono text-[8px]">
										HOST_TAG:
									</text>
									<text x="65" y="8" className="fill-white font-bold font-mono text-[8.5px] tracking-wide">
										{getProviderBrandName(targetedNode.provider.host).toUpperCase()}
									</text>

									<text x="0" y="21" className="fill-slate-400 font-mono text-[8px]">
										DOMAIN:
									</text>
									<text x="65" y="21" className="fill-slate-300 font-mono text-[7.5px] truncate">
										{targetedNode.provider.host}
									</text>
								</g>

								<line x1="0" y1="31" x2="211" y2="31" stroke="rgba(59, 130, 246, 0.15)" strokeWidth="0.8" />

								{/* Primary stats */}
								<g transform="translate(0, 44)">
									{/* Health score with color indicator */}
									<text x="0" y="8" className="fill-slate-400 font-mono text-[8px]">
										HEALTH:
									</text>
									<text x="72" y="8" className={`font-bold font-mono text-[9px] ${targetedNode.textColor}`}>
										{targetedNode.score}% / {targetedNode.score >= 85 ? "OPTIMAL" : targetedNode.score >= 50 ? "DEGRADED" : "CRITICAL"}
									</text>

									{/* Latency */}
									<text x="0" y="21" className="fill-slate-400 font-mono text-[8px]">
										LATENCY:
									</text>
									<text x="72" y="21" className="fill-slate-200 font-bold font-mono text-[8.5px]">
										{targetedNode.provider.ping_ms > 0 ? `${targetedNode.provider.ping_ms} ms` : "OFFLINE"}
									</text>

									{/* Connections */}
									<text x="0" y="34" className="fill-slate-400 font-mono text-[8px]">
										THREAD LOAD:
									</text>
									<text x="72" y="34" className="fill-cyan-400 font-bold font-mono text-[8.5px]">
										{targetedNode.provider.used_connections} / {targetedNode.provider.max_connections} THREADS
									</text>

									{/* Speed */}
									<text x="0" y="47" className="fill-slate-400 font-mono text-[8px]">
										NET SPEED:
									</text>
									<text x="72" y="47" className="fill-cyan-300 font-bold font-mono text-[9px]">
										{formatBytes(targetedNode.provider.current_speed_bytes_per_sec)}/s
									</text>

									{/* Total 24h byte count */}
									<text x="0" y="60" className="fill-slate-400 font-mono text-[8px]">
										DATA (24H):
									</text>
									<text x="72" y="60" className="fill-slate-200 font-mono text-[8px]">
										{formatBytes(targetedNode.provider.byte_count_24h || 0)}
									</text>

									{/* Error volume */}
									<text x="0" y="73" className="fill-slate-400 font-mono text-[8px]">
										ERRORS:
									</text>
									<text x="72" y="73" className={`font-bold font-mono text-[8px] ${targetedNode.provider.error_count > 0 ? "text-rose-400" : "text-slate-400"}`}>
										{targetedNode.provider.error_count} FAILURES
									</text>
								</g>

								<line x1="0" y1="126" x2="211" y2="126" stroke="rgba(59, 130, 246, 0.15)" strokeWidth="0.8" />

								{/* Interactive Action Indicators */}
								<g transform="translate(0, 134)">
									{targetedNode.provider.state === "connected" || targetedNode.provider.state === "active" ? (
										<g>
											<rect x="0" y="0" width="211" height="34" rx="3" className="fill-emerald-950/20 stroke-[0.8] stroke-emerald-500/10" />
											<ShieldCheck className="absolute x-2.5 y-2.5 h-3.5 w-3.5 text-emerald-400" />
											<text x="26" y="14" className="fill-emerald-400 font-bold font-mono text-[7.5px] tracking-wide animate-pulse">
												SECURE TUNNEL ACTIVE
											</text>
											<text x="26" y="24" className="fill-emerald-400/80 font-mono text-[7px]">
												SIGNAL FLOW STABLE // NO DATA DEVIATION
											</text>
										</g>
									) : (
										<g>
											<rect x="0" y="0" width="211" height="34" rx="3" className="fill-rose-950/20 stroke-[0.8] stroke-rose-500/15" />
											<AlertCircle className="absolute x-2.5 y-2.5 h-3.5 w-3.5 text-rose-400" />
											<text x="26" y="14" className="fill-rose-400 font-bold font-mono text-[7.5px] tracking-wide animate-pulse">
												LINK CONNECTION FAILURE
											</text>
											<text x="26" y="24" className="fill-rose-400/80 font-mono text-[7px] truncate max-w-[170px]">
												{targetedNode.provider.failure_reason || "HANDSHAKE TIMEOUT / DISCONNECTED"}
											</text>
										</g>
									)}
								</g>
							</g>
						)}

						{/* Footer status ticks */}
						<g transform="translate(557, 252)">
							<rect x="0" y="0" width="6" height="6" className="fill-blue-500/30" />
							<rect x="10" y="0" width="6" height="6" className="fill-blue-500/30" />
							<rect x="20" y="0" width="6" height="6" className="fill-blue-500/30" />
							<text x="35" y="6" className="fill-slate-500 font-mono text-[7.5px] uppercase tracking-wide">
								{targetedNode ? `STATUS_LOCK: ${targetedNode.provider.state.toUpperCase()}` : "LOCATING SEEDERS..."}
							</text>
						</g>
					</g>
				</svg>

				{/* Floating high-tech legends */}
				<div className="absolute bottom-3 left-4 flex flex-wrap gap-3 font-mono text-[9px] text-slate-400">
					<div className="flex items-center gap-1">
						<span className="h-1.5 w-1.5 rounded-full bg-emerald-400 shadow-[0_0_6px_rgba(52,211,153,0.7)]" />
						<span>Optimal (85%+)</span>
					</div>
					<div className="flex items-center gap-1">
						<span className="h-1.5 w-1.5 rounded-full bg-amber-400 shadow-[0_0_6px_rgba(251,191,36,0.7)]" />
						<span>Warning (50-84%)</span>
					</div>
					<div className="flex items-center gap-1">
						<span className="h-1.5 w-1.5 rounded-full bg-rose-500 shadow-[0_0_6px_rgba(244,63,94,0.7)]" />
						<span>Critical (&lt;50%)</span>
					</div>
				</div>
			</div>
		</div>
	);
}
