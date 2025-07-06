document.addEventListener("DOMContentLoaded", () => {
  const previews = document.querySelectorAll("[preview-for]");

  previews.forEach((previewEl) => {
    const selector = previewEl.getAttribute("preview-for");
    const prefix = previewEl.getAttribute("preview-prefix") ?? "";
    const targetEl = document.querySelector(selector);

    if (!targetEl) {
      console.warn(`No element matches selector "${selector}" for`, previewEl);
      return;
    }

    let currentObjectURL = null;

    let originURL = null;

    // Default sync for text inputs
    let sync = () => {
      let val = targetEl.value ?? targetEl.textContent ?? "";
      if (val) {
        val = prefix + val;
      }
      if ("href" in previewEl) {
        previewEl.href = val;
      } else if ("textContent" in previewEl) {
        previewEl.textContent = val;
      } else {
        previewEl.value = val;
      }
    };

    // Special sync for images
    if (
      previewEl.tagName.toLowerCase() === "img" &&
      targetEl.tagName.toLowerCase() === "input" &&
      targetEl.type === "file"
    ) {
      originURL = previewEl.attributes.src.value;
      sync = () => {
        const file = targetEl.files && targetEl.files[0];
        if (file) {
          // revoke previous URL to avoid memory leak
          if (currentObjectURL) URL.revokeObjectURL(currentObjectURL);
          currentObjectURL = URL.createObjectURL(file);
          previewEl.src = currentObjectURL;
        } else {
          previewEl.src = originURL;
        }
      };
    }

    if (previewEl.getAttribute("initial-sync") != "no") {
      sync();
    }

    targetEl.addEventListener("input", sync);
    targetEl.addEventListener("change", sync);
  });
});
