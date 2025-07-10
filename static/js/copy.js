
document.addEventListener('DOMContentLoaded', () => {
    document.querySelectorAll('.contact-element img[src$="copy.svg"]').forEach(btn => {
        btn.style.cursor = 'pointer';
        
        btn.addEventListener('click', () => {
        const span = btn.parentElement.querySelector('span');
        const text = span ? span.textContent.trim() : '';
        navigator.clipboard.writeText(text)
        });
    });
});
