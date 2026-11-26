export function BrandIcon({ size = 28, glowing = false, class: className = '' }) {
    return (
        <svg
            width={size}
            height={size}
            viewBox="0 0 32 32"
            fill="none"
            xmlns="http://www.w3.org/2000/svg"
            class={`brand-icon ${glowing ? 'brand-icon-glow' : ''} ${className}`}
            style={{ display: 'inline-block', verticalAlign: 'middle', flexShrink: 0, overflow: 'visible' }}
            aria-hidden="true"
        >
            <defs>
                {/* Premium Satin Chrome / Steel Grille Gradient */}
                <linearGradient id="grilleChrome" x1="11.4" y1="5" x2="20.6" y2="14" gradientUnits="userSpaceOnUse">
                    <stop offset="0%" stopColor="#ffffff" />
                    <stop offset="25%" stopColor="#e2e8f0" />
                    <stop offset="55%" stopColor="#94a3b8" />
                    <stop offset="85%" stopColor="#475569" />
                    <stop offset="100%" stopColor="#1e293b" />
                </linearGradient>

                {/* Dark Gunmetal Anodized Handle */}
                <linearGradient id="micHandleMetal" x1="12.7" y1="15.5" x2="19.3" y2="25.5" gradientUnits="userSpaceOnUse">
                    <stop offset="0%" stopColor="#475569" />
                    <stop offset="30%" stopColor="#334155" />
                    <stop offset="70%" stopColor="#1e293b" />
                    <stop offset="100%" stopColor="#0f172a" />
                </linearGradient>

                {/* Live "Hot Mic" Collar (Electric Coral into Gold) */}
                <linearGradient id="liveRingCollar" x1="12.4" y1="14" x2="19.6" y2="15.5" gradientUnits="userSpaceOnUse">
                    <stop offset="0%" stopColor="#ff2a5f" />
                    <stop offset="100%" stopColor="#f59e0b" />
                </linearGradient>

                {/* Glow Aura */}
                <radialGradient id="micGlowAura" cx="16" cy="9.5" r="9" gradientUnits="userSpaceOnUse">
                    <stop offset="0%" stopColor="#ff2a5f" stopOpacity="0.45" />
                    <stop offset="60%" stopColor="#f59e0b" stopOpacity="0.15" />
                    <stop offset="100%" stopColor="#ff2a5f" stopOpacity="0" />
                </radialGradient>
            </defs>

            {/* Stage Mic angled at -35 degrees */}
            <g transform="rotate(-35 16 16)">
                {/* Soft backdrop aura */}
                <circle cx="16" cy="9.5" r="8" fill="url(#micGlowAura)" />

                {/* Sound Wave 1 (Inner Arc - Electric Coral) */}
                <path
                    d="M12.4 2.4C13.5 1.4 18.5 1.4 19.6 2.4"
                    stroke="#ff2a5f"
                    strokeWidth="1.6"
                    strokeLinecap="round"
                    opacity="0.95"
                />

                {/* Sound Wave 2 (Outer Arc - Golden Amber) */}
                <path
                    d="M10.2 -0.8C12.2 -2.6 19.8 -2.6 21.8 -0.8"
                    stroke="#f59e0b"
                    strokeWidth="1.4"
                    strokeLinecap="round"
                    opacity="0.8"
                />

                {/* XLR Base Connector (Three-Pin Base) */}
                <path
                    d="M13.7 25.5H18.3L17.7 28.2H14.3L13.7 25.5Z"
                    fill="#1e293b"
                    stroke="#475569"
                    strokeWidth="0.6"
                />
                <circle cx="15.2" cy="27.2" r="0.45" fill="#64748b" />
                <circle cx="16.8" cy="27.2" r="0.45" fill="#64748b" />

                {/* Heavy Tapered Microphone Handle Body */}
                <path
                    d="M12.7 15.5H19.3L18.3 25.5H13.7L12.7 15.5Z"
                    fill="url(#micHandleMetal)"
                    stroke="#334155"
                    strokeWidth="0.75"
                    strokeLinejoin="round"
                />

                {/* Handle Ergonomic Grip Ribs */}
                <line x1="13.3" y1="22.5" x2="18.7" y2="22.5" stroke="#334155" strokeWidth="0.6" />
                <line x1="13.5" y1="24.0" x2="18.5" y2="24.0" stroke="#1e293b" strokeWidth="0.6" />

                {/* On/Off Slide Switch Bezel & Switch */}
                <rect x="15" y="17.2" width="2" height="4" rx="0.5" fill="#0f172a" stroke="#475569" strokeWidth="0.4" />
                {/* Switch Slider Button in "ON" Position with Live Indicator */}
                <rect x="15.3" y="17.5" width="1.4" height="1.8" rx="0.3" fill="#cbd5e1" />
                <circle cx="16" cy="18.4" r="0.4" fill="#ff2a5f" />

                {/* "Hot Mic" Live Collar Ring (Thick transition collar) */}
                <path
                    d="M12.3 14H19.7L19.3 15.5H12.7L12.3 14Z"
                    fill="url(#liveRingCollar)"
                    stroke="#ff2a5f"
                    strokeWidth="0.4"
                />

                {/* Dynamic Mic Capsule: Lower Tapered Basket Collar */}
                <path
                    d="M11.5 10.2H20.5L19.7 14H12.3L11.5 10.2Z"
                    fill="url(#grilleChrome)"
                    stroke="#475569"
                    strokeWidth="0.6"
                />

                {/* Dynamic Mic Capsule: Upper Hemispherical Bullet Dome */}
                <path
                    d="M11.4 10.2C11.4 6 13.4 4.2 16 4.2C18.6 4.2 20.6 6 20.6 10.2H11.4Z"
                    fill="url(#grilleChrome)"
                    stroke="#475569"
                    strokeWidth="0.75"
                />

                {/* Capsule Equator Center Metal Band (The SM58 Signature Rib) */}
                <rect
                    x="11.2"
                    y="9.6"
                    width="9.6"
                    height="1.2"
                    rx="0.4"
                    fill="#ffffff"
                    stroke="#cbd5e1"
                    strokeWidth="0.4"
                />

                {/* Grille Wire Mesh Detailing: Horizontal Lattice Curves */}
                <path
                    d="M12.5 7.2C13.5 6.2 18.5 6.2 19.5 7.2"
                    stroke="#ffffff"
                    strokeWidth="0.65"
                    strokeLinecap="round"
                    opacity="0.8"
                />
                <path
                    d="M12.2 12.2H19.8"
                    stroke="#334155"
                    strokeWidth="0.6"
                    opacity="0.6"
                />

                {/* Grille Center Vertical Seam */}
                <line
                    x1="16"
                    y1="4.5"
                    x2="16"
                    y2="13.8"
                    stroke="#ffffff"
                    strokeWidth="0.7"
                    strokeDasharray="1.2 0.8"
                    opacity="0.55"
                />

                {/* Left Chrome Highlight Strip */}
                <path
                    d="M13 5.8C12.3 7 12.3 8.6 12.6 10"
                    stroke="#ffffff"
                    strokeWidth="0.7"
                    strokeLinecap="round"
                    opacity="0.85"
                />
            </g>
        </svg>
    )
}
