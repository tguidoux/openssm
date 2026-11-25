// OpenSSM UI JavaScript — progressive enhancement only; every page works without it.
(function() {
'use strict';

// Theme toggle (persisted)
const savedTheme = localStorage.getItem('openssm-theme');
if (savedTheme) {
    document.documentElement.setAttribute('data-theme', savedTheme);
}
document.getElementById('theme-toggle')?.addEventListener('click', function() {
    const next = document.documentElement.getAttribute('data-theme') === 'dark' ? 'light' : 'dark';
    document.documentElement.setAttribute('data-theme', next);
    localStorage.setItem('openssm-theme', next);
});

// Confirmation dialogs for destructive forms: <form data-confirm="...">
document.querySelectorAll('form[data-confirm]').forEach(function(form) {
    form.addEventListener('submit', function(e) {
        if (!confirm(form.getAttribute('data-confirm'))) {
            e.preventDefault();
        }
    });
});

// Copy buttons: <button data-copy="element-id">
document.querySelectorAll('[data-copy]').forEach(function(btn) {
    btn.addEventListener('click', function() {
        const el = document.getElementById(btn.getAttribute('data-copy'));
        if (!el || !navigator.clipboard) return;
        navigator.clipboard.writeText(el.textContent).then(function() {
            const original = btn.textContent;
            btn.textContent = 'Copied!';
            setTimeout(function() { btn.textContent = original; }, 2000);
        });
    });
});

// Show the KMS key picker only for SecureString parameters.
const typeSelect = document.getElementById('type');
const kmsGroup = document.getElementById('kms-group');
if (typeSelect && kmsGroup) {
    const sync = function() { kmsGroup.hidden = typeSelect.value !== 'SecureString'; };
    typeSelect.addEventListener('change', sync);
    sync();
}

})();
