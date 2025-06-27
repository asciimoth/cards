document.addEventListener("DOMContentLoaded", () => {
  document.querySelectorAll("[hide-when-no-content]").forEach((host) => {
    const selector = host.getAttribute("hide-when-no-content");
    const target = document.querySelector(selector);
    if (!target) {
      console.warn(`No element matches selector "${selector}" for`, host);
      return;
    }

    // Checks “has content” and collapses host if empty
    const updateVisibility = () => {
      let hasContent = false;
      const tag = target.tagName.toUpperCase();

      if (tag === "IMG") {
        hasContent = Boolean(target.attributes.src.value);
      } else if (["INPUT", "TEXTAREA", "SELECT"].includes(tag)) {
        hasContent = Boolean(target.value);
      } else {
        hasContent = Boolean(target.textContent);
      }

      host.style.visibility = hasContent ? "" : "collapse";
      host.style.display = hasContent ? "" : "none";
    };

    // Initial check
    updateVisibility();

    // 1) Listen for form-control changes
    if (
      ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName.toUpperCase())
    ) {
      target.addEventListener("input", updateVisibility);
      target.addEventListener("change", updateVisibility);
    }

    // 2) Observe attribute changes (e.g. img.src)
    const attrObserver = new MutationObserver((muts) => {
      for (let m of muts) {
        if (m.type === "attributes") {
          updateVisibility();
          break;
        }
      }
    });
    attrObserver.observe(target, {
      attributes: true,
      attributeFilter:
        target.tagName.toUpperCase() === "IMG" ? ["src"] : undefined,
    });

    // 3) Observe text‐content changes for other elements
    if (
      !["IMG", "INPUT", "TEXTAREA", "SELECT"].includes(
        target.tagName.toUpperCase(),
      )
    ) {
      const textObserver = new MutationObserver(() => updateVisibility());
      textObserver.observe(target, {
        childList: true,
        characterData: true,
        subtree: true,
      });
    }
  });
});
