const fileInput = document.getElementById("input-avatar-precrop");
const croppedInput = document.getElementById("input-avatar");
const image = document.getElementById("image-preview");
const container = document.getElementById("preview-container");
const ok = document.getElementById("avatar-preview-ok");
let cropper;

// Helper to update cropped file field
function updateCroppedFile() {
    if (!cropper) return;
    let canvas = cropper.getCroppedCanvas();
    canvas.toBlob(
        (blob) => {
            // Derive base name, swap extension to .webp
            const origName =
                fileInput.files[0]?.name || "cropped.png";
            const baseName = origName.replace(/\.\w+$/, "");
            const webpName = `${baseName}.webp`;

            // Create the new File with MIME type image/webp
            const croppedFile = new File([blob], webpName, {
                type: "image/webp",
            });

            // Stick it into the hidden input
            const dt = new DataTransfer();
            dt.items.add(croppedFile);
            croppedInput.files = dt.files;

            // If you have any listeners on the input, trigger them
            const ev = new Event("change", {
                bubbles: true,
                cancelable: true,
            });
            croppedInput.dispatchEvent(ev);
        },
        "image/webp",
        0.5,
    );
}

fileInput.addEventListener("change", (e) => {
    const file = e.target.files[0];
    if (!file) return;
    const url = URL.createObjectURL(file);

    image.src = url;
    image.style.display = "block";

    if (cropper) cropper.destroy();
    cropper = new Cropper(image, {
        aspectRatio: 1,
        viewMode: 1,
        autoCrop: true,
        autoCropArea: 0.8,
        cropend: updateCroppedFile,
        ready: updateCroppedFile,
    });
});

const logoPre = document.getElementById("input-logo-pre");
const logoDest = document.getElementById("input-logo");

logoPre.addEventListener("change", async () => {
    const file = logoPre.files[0];
    if (!file) return;

    // 1) Load into an Image object
    const img = new Image();
    img.src = URL.createObjectURL(file);
    await img.decode();

    // 2) Draw to a temporary canvas
    const canvas = document.createElement("canvas");
    canvas.width = img.naturalWidth;
    canvas.height = img.naturalHeight;
    canvas.getContext("2d").drawImage(img, 0, 0);

    // 3) Export as WebP @80% quality
    canvas.toBlob(
        (blob) => {
            // Build new filename with .webp extension
            const base = file.name.replace(/\.\w+$/, "");
            const webpName = `${base}.webp`;

            // Create the WebP File and stuff it into the destination input
            const webpFile = new File([blob], webpName, {
                type: "image/webp",
            });
            const dt = new DataTransfer();
            dt.items.add(webpFile);
            logoDest.files = dt.files;

            // Trigger any change listeners downstream
            logoDest.dispatchEvent(
                new Event("change", {
                    bubbles: true,
                    cancelable: true,
                }),
            );

            // Clean up
            URL.revokeObjectURL(img.src);
        },
        "image/webp",
        0.0,
    );
});

ok.addEventListener('click', () => {
    container.style.visibility = "collapse";
    container.style.display = "none";
});
