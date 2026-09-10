/**
 * Ordered rule list editor, shared by the source modal and the filter profile
 * modal. A rule is { field, action, pattern, note }; the first rule whose
 * pattern matches a stream decides whether it is imported, and the owner's
 * default settles what nothing matched.
 */

const RULE_FIELDS = [
    { value: 'group', label: 'Group' },
    { value: 'name', label: 'Name' },
    { value: 'url', label: 'URL' },
    { value: 'any', label: 'Any' },
];
const RULE_ACTIONS = [
    { value: 'include', label: 'Keep' },
    { value: 'exclude', label: 'Drop' },
];

/**
 * Escapes a literal string for use inside a regular expression, so a group
 * label with brackets or a plus sign can be turned into a rule verbatim.
 * @param {string} text
 * @returns {string}
 */
function escapeRegex(text) {
    return String(text ?? '').replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/**
 * Creates a rule editor bound to a container element.
 * @param {string} containerId - Element the rows are rendered into
 * @param {Object} options
 * @param {Function} options.onChange - Called after any edit
 * @param {Function} [options.onInput] - Called on every keystroke in a pattern, so a preview can follow the typing
 * @returns {Object} editor handle
 */
function createRuleEditor(containerId, options = {}) {
    const editor = {
        rules: [],
        stats: null,     // [{ index, matched, decided, origin }] from the last preview
        profileRules: [], // read-only rules contributed by a profile, shown above
        profileName: '',
    };

    /** @returns {HTMLElement} */
    const container = () => document.getElementById(containerId);
    const changed = () => { render(); if (options.onChange) options.onChange(); };

    /**
     * Replaces the rules being edited.
     * @param {Array<Object>} rules
     */
    editor.setRules = (rules) => {
        editor.rules = (rules || []).map(r => ({
            field: r.field || 'group',
            action: r.action || 'include',
            pattern: r.pattern || '',
            note: r.note || '',
        }));
        editor.stats = null;
        render();
    };

    /**
     * The rules as the API stores them. Patterns are sent exactly as typed:
     * whitespace is significant in a regular expression, and a trailing run of
     * spaces is how some providers mark a family of groups.
     * @returns {Array<Object>}
     */
    editor.getRules = () => editor.rules
        .filter(r => r.pattern !== '')
        .map(r => ({ field: r.field, action: r.action, pattern: r.pattern, note: r.note.trim() }));

    /**
     * Attaches per-rule counts from a preview report.
     * @param {Array<Object>|null} ruleStats
     */
    editor.setStats = (ruleStats) => {
        editor.stats = ruleStats || null;
        render();
    };

    /**
     * Shows the rules a profile contributes ahead of these, for context.
     * @param {string} name
     * @param {Array<Object>} rules
     */
    editor.setProfileRules = (name, rules) => {
        editor.profileName = name || '';
        editor.profileRules = rules || [];
        render();
    };

    /**
     * Appends a rule and focuses its pattern box.
     * @param {Object} rule
     */
    editor.addRule = (rule) => {
        editor.rules.push({
            field: rule.field || 'group',
            action: rule.action || 'include',
            pattern: rule.pattern || '',
            note: rule.note || '',
        });
        changed();
        const inputs = container().querySelectorAll('.rule-pattern');
        const last = inputs[inputs.length - 1];
        if (last) { last.focus(); last.select(); }
    };

    /** @returns {boolean} whether every pattern compiles in the browser */
    editor.validate = () => {
        let ok = true;
        container().querySelectorAll('.rule-pattern').forEach(input => {
            if (!validateRegexInput(input)) ok = false;
        });
        return ok;
    };

    /**
     * Stats for one rule of this editor, offset past the profile's rules,
     * which occupy the first positions of the effective list.
     * @param {number} index
     * @returns {Object|null}
     */
    function statsFor(index) {
        if (!editor.stats) return null;
        return editor.stats[editor.profileRules.length + index] || null;
    }

    /**
     * One editable row.
     * @param {Object} rule
     * @param {number} i
     * @returns {string} HTML
     */
    function row(rule, i) {
        const stat = statsFor(i);
        const counts = stat
            ? `<span class="text-xs tabular-nums ${stat.decided ? 'text-kptv-blue' : 'text-gray-500'}" title="Streams this rule decided, and how many its pattern matched in total">
                   ${formatCount(stat.decided)}<span class="text-gray-500"> / ${formatCount(stat.matched)}</span>
               </span>`
            : '<span class="text-xs text-gray-600">—</span>';
        const shadowed = stat && stat.decided === 0 && stat.matched > 0
            ? '<span class="text-xs text-orange-400" title="An earlier rule already decided every stream this one matches">shadowed</span>'
            : '';
        const dead = stat && stat.matched === 0
            ? '<span class="text-xs text-orange-400" title="Nothing in the catalog matches this pattern">no match</span>'
            : '';

        return `
        <div class="rule-row" data-index="${i}">
            <div class="flex flex-col gap-0.5 flex-shrink-0">
                <button type="button" class="rule-move text-gray-500 hover:text-gray-200 leading-none" data-dir="-1" title="Move up" ${i === 0 ? 'disabled' : ''}>▲</button>
                <button type="button" class="rule-move text-gray-500 hover:text-gray-200 leading-none" data-dir="1" title="Move down" ${i === editor.rules.length - 1 ? 'disabled' : ''}>▼</button>
            </div>
            <span class="text-xs text-gray-500 tabular-nums w-4 text-right flex-shrink-0">${i + 1}</span>
            <select class="rule-action rule-select ${rule.action === 'exclude' ? 'text-red-300' : 'text-green-300'}" title="What a match does">
                ${RULE_ACTIONS.map(a => `<option value="${a.value}" ${a.value === rule.action ? 'selected' : ''}>${a.label}</option>`).join('')}
            </select>
            <select class="rule-field rule-select" title="Which value the pattern is matched against">
                ${RULE_FIELDS.map(f => `<option value="${f.value}" ${f.value === rule.field ? 'selected' : ''}>${f.label}</option>`).join('')}
            </select>
            <input type="text" class="rule-pattern filter-input flex-1 min-w-[8rem] font-mono text-sm" value="${escapeAttr(rule.pattern)}"
                placeholder="regular expression" spellcheck="false" autocomplete="off">
            <input type="text" class="rule-note filter-input w-32 text-sm hidden sm:block" value="${escapeAttr(rule.note)}"
                placeholder="note" autocomplete="off">
            <span class="flex items-center gap-2 flex-shrink-0">
                ${shadowed || dead}
                ${counts}
                <button type="button" class="rule-remove text-gray-500 hover:text-red-400" title="Remove this rule">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"></path>
                    </svg>
                </button>
            </span>
        </div>`;
    }

    /** Rules inherited from the profile, shown read-only above the editable ones. */
    function profileRows() {
        if (!editor.profileRules.length) return '';
        const rows = editor.profileRules.map((rule, i) => {
            const stat = editor.stats ? editor.stats[i] : null;
            return `
            <div class="rule-row opacity-70" title="From the profile &quot;${escapeAttr(editor.profileName)}&quot;">
                <span class="text-xs text-gray-500 tabular-nums w-4 text-right flex-shrink-0">${i + 1}</span>
                <span class="text-xs px-1.5 py-0.5 rounded border border-kptv-border ${rule.action === 'exclude' ? 'text-red-300' : 'text-green-300'}">${rule.action === 'exclude' ? 'Drop' : 'Keep'}</span>
                <span class="text-xs px-1.5 py-0.5 rounded border border-kptv-border text-gray-300">${escapeHtml((RULE_FIELDS.find(f => f.value === rule.field) || RULE_FIELDS[0]).label)}</span>
                <code class="flex-1 min-w-0 text-sm text-gray-300 truncate whitespace-pre" title="${escapeAttr(JSON.stringify(rule.pattern))}">${escapeHtml(rule.pattern)}</code>
                ${stat ? `<span class="text-xs tabular-nums ${stat.decided ? 'text-kptv-blue' : 'text-gray-500'}">${formatCount(stat.decided)}<span class="text-gray-500"> / ${formatCount(stat.matched)}</span></span>` : ''}
            </div>`;
        }).join('');
        return `
        <div class="px-3 py-1.5 text-xs text-gray-400 bg-kptv-gray-light border-b border-kptv-border">
            From profile <span class="text-gray-200">${escapeHtml(editor.profileName)}</span> — evaluated first, edit it under Sources → Filter Profiles
        </div>${rows}`;
    }

    /** Renders every row from state. */
    function render() {
        const el = container();
        if (!el) return;
        const own = editor.rules.length
            ? editor.rules.map(row).join('')
            : `<div class="px-3 py-4 text-sm text-gray-400">No rules yet. ${editor.profileRules.length ? 'The profile above already decides; add a rule to refine it.' : 'Add one, or pick a group below.'}</div>`;
        el.innerHTML = profileRows() + own;
        el.querySelectorAll('.rule-pattern').forEach(validateRegexInput);
    }

    // one delegated listener set per editor, wired once
    const el = container();
    el.addEventListener('input', (e) => {
        const rowEl = e.target.closest('.rule-row');
        if (!rowEl || rowEl.dataset.index === undefined) return;
        const rule = editor.rules[Number(rowEl.dataset.index)];
        if (e.target.classList.contains('rule-pattern')) {
            rule.pattern = e.target.value;
            validateRegexInput(e.target);
            if (options.onInput) options.onInput();
        } else if (e.target.classList.contains('rule-note')) {
            rule.note = e.target.value;
        }
    });
    el.addEventListener('change', (e) => {
        const rowEl = e.target.closest('.rule-row');
        if (!rowEl || rowEl.dataset.index === undefined) return;
        const rule = editor.rules[Number(rowEl.dataset.index)];
        if (e.target.classList.contains('rule-field')) {
            rule.field = e.target.value;
            changed();
        } else if (e.target.classList.contains('rule-action')) {
            rule.action = e.target.value;
            changed();
        } else if (e.target.classList.contains('rule-pattern')) {
            // a finished edit is worth re-evaluating
            changed();
        }
    });
    el.addEventListener('click', (e) => {
        const rowEl = e.target.closest('.rule-row');
        if (!rowEl || rowEl.dataset.index === undefined) return;
        const i = Number(rowEl.dataset.index);
        if (e.target.closest('.rule-remove')) {
            editor.rules.splice(i, 1);
            changed();
            return;
        }
        const move = e.target.closest('.rule-move');
        if (move) {
            const to = i + Number(move.dataset.dir);
            if (to < 0 || to >= editor.rules.length) return;
            [editor.rules[i], editor.rules[to]] = [editor.rules[to], editor.rules[i]];
            changed();
        }
    });

    render();
    return editor;
}
