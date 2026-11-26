import { useEffect, useRef } from 'preact/hooks'
import './LetterBar.css'

const LETTERS = ['#', ...'ABCDEFGHIJKLMNOPQRSTUVWXYZ']

// LetterBar selects the current letter page in a public catalog view.
export function LetterBar({ activeLetter, letterCounts, onSelectLetter, itemType = 'songs' }) {
    const scrollRef = useRef(null)

    useEffect(() => {
        const activeElement = scrollRef.current?.querySelector(`[data-letter="${activeLetter}"]`)
        activeElement?.scrollIntoView({ behavior: 'smooth', block: 'nearest', inline: 'center' })
    }, [activeLetter])

    return (
        <nav class="letter-bar" ref={scrollRef} aria-label={`Browse by ${itemType} first letter`}>
            {LETTERS.map((letter) => {
                const count = letterCounts[letter] || 0
                const hasItems = count > 0
                return (
                    <button
                        key={letter}
                        type="button"
                        data-letter={letter}
                        class={`letter-pill ${activeLetter === letter ? 'active' : ''}`}
                        disabled={!hasItems}
                        onClick={() => onSelectLetter(letter)}
                        aria-label={`${letter}: ${count} ${itemType}`}
                    >
                        <span>{letter}</span>
                        {hasItems && <span class="letter-count">{count > 999 ? `${Math.round(count / 100) / 10}k` : count}</span>}
                    </button>
                )
            })}
        </nav>
    )
}
