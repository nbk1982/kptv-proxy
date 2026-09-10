/**
 * Filter profiles: rule sets stored on their own so several sources can share
 * one. Lives as a sub-tab under Sources, and is edited in its own modal with
 * the same rule editor the source modal uses.
 */

let allFilterProfiles = [];
let profileEditor = null;
let profileEditingId = null;
let profilePreviewTimer = null;
let profilePreviewLoading = false;

/**
 * Fetches every profile and renders the list.
 * @returns {Promise<void>}
 */
async function loadFilterProfiles() {
    try {
        allFilterProfiles = await apiCall('/api/filter-profiles');
    } catch (error) {
        document.getElementById('filter-profiles-container').innerHTML =
            '<div class="bg-orange-900/20 border border-orange-600 text-orange-100 px-4 py-3 rounded">Failed to load filter profiles</div>';
        return;
    }
    renderFilterProfiles();
    renderProfileOptions();
}

/**
 * Fills the source modal's profile selector from the loaded profiles,
 * preserving the current selection.
 */
function renderProfileOptions() {
    const select = document.getElementById('source-filter-profile');
    if (!select) return;
    const current = select.value;
    select.innerHTML = '<option value="">No profile</option>' +
        allFilterProfiles.map(p => `<option value="${escapeAttr(p.name)}">${escapeHtml(p.name)} (${p.rules.length} rule${p.rules.length === 1 ? '' : 's'})</option>`).join('');
    select.value = allFilterProfiles.some(p => p.name === current) ? current : '';
}

/** Renders the profile cards. */
function renderFilterProfiles() {
    const container = document.getElementById('filter-profiles-container');
    if (!container) return;

    if (!allFilterProfiles.length) {
        container.innerHTML = `<div class="bg-kptv-gray border border-kptv-border rounded p-4 text-sm text-gray-400">
            No filter profiles yet. A profile is a rule set you can attach to several sources, so two providers with the same
            group naming are filtered the same way and one edit updates both.</div>`;
        return;
    }

    container.innerHTML = allFilterProfiles.map((p, index) => {
        const summary = p.rules.length
            ? p.rules.slice(0, 4).map(r => `<span class="px-2 py-0.5 rounded text-xs border border-kptv-border ${r.action === 'exclude' ? 'text-red-300' : 'text-green-300'}"
                    title="${escapeAttr(JSON.stringify(r.pattern))}">
                    ${r.action === 'exclude' ? 'Drop' : 'Keep'} ${escapeHtml(r.field)} <code class="whitespace-pre">${escapeHtml(r.pattern)}</code></span>`).join('')
              + (p.rules.length > 4 ? `<span class="text-xs text-gray-400">+${p.rules.length - 4} more</span>` : '')
            : '<span class="text-xs text-gray-500">No rules</span>';
        return `
        <div class="source-item">
            <div class="flex justify-between items-start mb-2 gap-3">
                <div class="min-w-0">
                    <h4 class="text-lg font-semibold mb-1">${escapeHtml(p.name)}</h4>
                    <div class="text-sm text-gray-400">
                        ${p.rules.length} rule${p.rules.length === 1 ? '' : 's'} ·
                        unmatched streams are <span class="text-gray-200">${p.default === 'drop' ? 'dropped' : 'kept'}</span>
                    </div>
                </div>
                <div class="text-sm text-gray-400 flex-shrink-0 text-right">
                    ${p.sources.length
                ? `Used by ${p.sources.map(s => escapeHtml(s)).join(', ')}`
                : '<span class="text-gray-500">Not used yet</span>'}
                </div>
            </div>
            <div class="flex flex-wrap gap-1 mb-3">${summary}</div>
            <div class="mt-4 pt-4 border-t border-kptv-border flex gap-2">
                <button class="px-3 py-1 bg-kptv-blue hover:bg-kptv-blue-light rounded text-sm transition-colors" onclick="showProfileModal(${index})">Edit</button>
                <button class="px-3 py-1 bg-kptv-gray-light border border-kptv-border hover:bg-kptv-border rounded text-sm transition-colors" onclick="duplicateProfile(${index})">Duplicate</button>
                <button class="px-3 py-1 bg-red-700 hover:bg-red-600 rounded text-sm transition-colors" onclick="deleteProfile(${index})">Delete</button>
            </div>
        </div>`;
    }).join('');
}

/** Wires the profile modal once at startup. */
function initFilterProfiles() {
    profileEditor = createRuleEditor('profile-rules', { onChange: () => scheduleProfilePreview(), onInput: () => scheduleProfilePreview() });

    document.getElementById('add-filter-profile-btn').addEventListener('click', () => showProfileModal());
    document.getElementById('save-profile-btn').addEventListener('click', () => saveProfile());
    document.getElementById('profile-add-rule-btn').addEventListener('click', () => profileEditor.addRule({ field: 'group', action: 'include' }));
    document.getElementById('profile-rules').addEventListener('change', () => renderProfileDefaultHint());
    document.getElementById('profile-default').addEventListener('click', (e) => {
        const btn = e.target.closest('.seg-btn');
        if (!btn) return;
        document.querySelectorAll('#profile-default .seg-btn').forEach(b => b.classList.toggle('active', b === btn));
        renderProfileDefaultHint();
        scheduleProfilePreview();
    });
    document.getElementById('profile-test-source').addEventListener('change', () => runProfilePreview());
    document.getElementById('profile-preview-btn').addEventListener('click', () => runProfilePreview());
    document.getElementById('manage-profiles-link').addEventListener('click', (e) => {
        e.preventDefault();
        hideModal('source-modal');
        closeFilterPanel();
        document.querySelector('.srctype-tab-btn[data-tab="2"]').click();
    });
}

/**
 * Opens the profile modal, empty for a new profile.
 * @param {number|null} index - Index into allFilterProfiles
 */
function showProfileModal(index = null) {
    const profile = index === null ? null : allFilterProfiles[index];
    profileEditingId = profile ? profile.id : null;

    document.getElementById('profile-modal-title').textContent = profile ? 'Edit Filter Profile' : 'New Filter Profile';
    document.getElementById('profile-name').value = profile ? profile.name : '';
    document.querySelectorAll('#profile-default .seg-btn').forEach(b => {
        b.classList.toggle('active', b.dataset.default === ((profile && profile.default) || 'keep'));
    });
    profileEditor.setRules(profile ? profile.rules : []);
    profileEditor.setStats(null);
    renderProfileDefaultHint();
    document.getElementById('profile-preview-summary').textContent =
        'Pick a source to see what these rules would keep in its catalog.';

    const select = document.getElementById('profile-test-source');
    const sources = (adminConfig && adminConfig.sources) || [];
    select.innerHTML = '<option value="">Test against…</option>' +
        sources.map(s => `<option value="${escapeAttr(s.url)}">${escapeHtml(s.name)}</option>`).join('');

    showModal('profile-modal');
}

/**
 * Opens the modal pre-filled from an existing profile, as a new one.
 * @param {number} index
 */
function duplicateProfile(index) {
    const profile = allFilterProfiles[index];
    showProfileModal(index);
    profileEditingId = null;
    document.getElementById('profile-modal-title').textContent = 'New Filter Profile';
    document.getElementById('profile-name').value = `${profile.name} copy`;
}

/** Warns when the profile's rules and default would import nothing. */
function renderProfileDefaultHint() {
    const hint = document.getElementById('profile-default-hint');
    if (!hint) return;
    const active = document.querySelector('#profile-default .seg-btn.active');
    const drop = active && active.dataset.default === 'drop';
    const keeps = profileEditor.getRules().filter(r => r.action === 'include').length;
    const warn = drop && keeps === 0;
    hint.textContent = warn ? 'With no Keep rule, dropping by default imports nothing.' : '';
    hint.classList.toggle('text-orange-400', warn);
}

/** @returns {Object} the profile as the API stores it */
function readProfileForm() {
    const active = document.querySelector('#profile-default .seg-btn.active');
    return {
        name: document.getElementById('profile-name').value.trim(),
        default: active ? active.dataset.default : 'keep',
        rules: profileEditor.getRules(),
    };
}

/**
 * Creates or updates the profile being edited.
 * @returns {Promise<void>}
 */
async function saveProfile() {
    const profile = readProfileForm();
    if (!profile.name) {
        showNotification('The profile needs a name', 'danger');
        return;
    }
    if (!profileEditor.validate()) {
        showNotification('Fix the highlighted pattern first', 'danger');
        return;
    }

    try {
        if (profileEditingId === null) {
            await apiCall('/api/filter-profiles', { method: 'POST', body: JSON.stringify(profile), quiet: true });
        } else {
            await apiCall(`/api/filter-profiles/${profileEditingId}`, { method: 'PUT', body: JSON.stringify(profile), quiet: true });
        }
    } catch (error) {
        showNotification('Failed to save profile: ' + error.message, 'danger');
        return;
    }

    hideModal('profile-modal');
    showNotification('Filter profile saved', 'success');
    await loadFilterProfiles();
    loadGlobalSettings();
    // sources attached to it are filtered by the new rules on the next import
    if (allFilterProfiles.some(p => p.name === profile.name && p.sources.length)) {
        triggerImport();
    }
}

/**
 * Deletes a profile, detaching the sources that use it once confirmed.
 * @param {number} index
 * @returns {Promise<void>}
 */
async function deleteProfile(index) {
    const profile = allFilterProfiles[index];
    if (!confirm(`Delete the filter profile "${profile.name}"?`)) return;

    try {
        await apiCall(`/api/filter-profiles/${profile.id}`, { method: 'DELETE', quiet: true });
    } catch (error) {
        if (/HTTP 409/.test(error.message)) {
            if (!confirm(`"${profile.name}" is used by ${profile.sources.join(', ')}. Delete it anyway and leave those sources with their own rules?`)) return;
            try {
                await apiCall(`/api/filter-profiles/${profile.id}?force=true`, { method: 'DELETE', quiet: true });
            } catch (forced) {
                showNotification('Failed to delete profile: ' + forced.message, 'danger');
                return;
            }
        } else {
            showNotification('Failed to delete profile: ' + error.message, 'danger');
            return;
        }
    }

    showNotification('Filter profile deleted', 'success');
    const affected = profile.sources.length > 0;
    await loadFilterProfiles();
    loadGlobalSettings();
    if (affected) triggerImport();
}

/** Re-runs the profile preview shortly after an edit. */
function scheduleProfilePreview() {
    if (!document.getElementById('profile-test-source').value) return;
    clearTimeout(profilePreviewTimer);
    profilePreviewTimer = setTimeout(() => runProfilePreview(), 700);
}

/**
 * Evaluates the profile being edited against a chosen source, without saving
 * either. The source keeps its own rules, so the number shown is what that
 * source would really import.
 * @returns {Promise<void>}
 */
async function runProfilePreview() {
    const url = document.getElementById('profile-test-source').value;
    const summary = document.getElementById('profile-preview-summary');
    if (!url) {
        summary.textContent = 'Pick a source to see what these rules would keep in its catalog.';
        profileEditor.setStats(null);
        return;
    }
    if (profilePreviewLoading) return;
    if (!profileEditor.validate()) {
        summary.textContent = 'Fix the highlighted pattern to preview.';
        return;
    }

    const source = ((adminConfig && adminConfig.sources) || []).find(s => s.url === url);
    if (!source) return;
    const profile = readProfileForm();
    if (!profile.name) {
        summary.textContent = 'Name the profile to preview it.';
        return;
    }

    profilePreviewLoading = true;
    document.getElementById('profile-preview-spinner').hidden = false;
    summary.textContent = 'Evaluating…';
    try {
        const result = await apiCall('/api/sources/preview', {
            method: 'POST',
            // the source is sent as it is stored but pointed at this profile,
            // so the preview measures the rules being edited
            body: JSON.stringify({ source: { ...source, filterProfile: profile.name }, profile, force: false }),
            quiet: true,
        });
        const report = result.report;
        summary.innerHTML = `<span class="text-kptv-blue font-semibold">${formatCount(report.kept)}</span> of ${formatCount(report.total)} streams kept in
            <span class="text-gray-200">${escapeHtml(source.name)}</span> · unmatched: ${formatCount(report.defaultDecided)} ${report.defaultAction === 'drop' ? 'dropped' : 'kept'}`;
        profileEditor.setStats(report.rules);
    } catch (error) {
        summary.textContent = 'Preview failed: ' + error.message;
    } finally {
        profilePreviewLoading = false;
        document.getElementById('profile-preview-spinner').hidden = true;
    }
}
