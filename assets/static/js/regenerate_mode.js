// Mode switching for the regeneration forms (per cut and per section).
//
// They behave the same except that only the cut form has "the full-regeneration input field", so
// one file serves both pages, branching on whether the element exists. The two templates used to
// each carry nearly the same 20 lines.
(() => {
    'use strict';

    const modeRadios = document.querySelectorAll('input[name="mode"]');
    if (modeRadios.length === 0) return;

    const editFields = document.getElementById('edit-fields');
    const editPrompt = document.getElementById('edit_prompt');
    if (!editFields || !editPrompt) return;

    // The per-section regeneration has no such field.
    const regenerateFields = document.getElementById('regenerate-fields');

    function applyMode(radio) {
        const isEdit = radio.value === 'edit' && radio.checked;
        regenerateFields?.classList.toggle('d-none', isEdit);
        editFields.classList.toggle('d-none', !isEdit);
        editPrompt.required = isEdit;
        // Disabled fields are excluded from form submission, so leftover edit_prompt text
        // can't be submitted after switching back to full-regenerate mode.
        editPrompt.disabled = !isEdit;
    }

    modeRadios.forEach((radio) => {
        radio.addEventListener('change', () => applyMode(radio));
        if (radio.checked) {
            applyMode(radio);
        }
    });
})();
