// History detail (/history/{jobID}). Narrows the cut list to match the selected generation target.
//
// Delete and copy ride on data attributes shared by every page (data-delete-url / data-copy-text),
// so this narrowing is the only thing specific to this page.
(() => {
    'use strict';

    const target = document.getElementById('gen-target');
    if (!target) return;

    target.addEventListener('change', () => {
        const selected = target.value;
        document.querySelectorAll('[data-section-group]').forEach((group) => {
            group.classList.toggle('d-none', selected !== 'full' && group.dataset.sectionGroup !== selected);
        });
    });
})();
