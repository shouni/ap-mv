// 再生成フォーム（カット単位・セクション単位）のモード切り替え。
//
// カット側だけが「フル再生成の入力欄」を持つ以外は同じ挙動なので、要素の有無で分岐して
// 1 ファイルを両方のページで使います。以前は 2 つのテンプレートがほぼ同じ 20 行を
// それぞれ抱えていました。
(() => {
    'use strict';

    const modeRadios = document.querySelectorAll('input[name="mode"]');
    if (modeRadios.length === 0) return;

    const editFields = document.getElementById('edit-fields');
    const editPrompt = document.getElementById('edit_prompt');
    if (!editFields || !editPrompt) return;

    // セクション単位の再生成にはこの欄がありません。
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
