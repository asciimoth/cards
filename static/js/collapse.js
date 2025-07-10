document.addEventListener("DOMContentLoaded", () => {
  document.querySelectorAll("[hide-when-no-content]").forEach((host) => {
    const selector = host.getAttribute("hide-when-no-content");
    const targets = document.querySelectorAll(selector);
    if (targets.len < 1) {
      console.warn(`No element matches selector "${selector}" for`, host);
      return;
    }

    console.log(host, selector, targets);

    // Checks “has content” and collapses host if empty
    const updateVisibility = () => {
      let hasContent = false;

      targets.forEach((target) => {
        const tag = target.tagName.toUpperCase();

        if (tag === "IMG") {
          hasContent = hasContent || Boolean(target.attributes.src.value);
        } else if (["INPUT", "TEXTAREA", "SELECT"].includes(tag)) {
          hasContent = hasContent || Boolean(target.value);
        } else {
          hasContent = hasContent || Boolean(target.textContent);
          }
      });

      host.style.visibility = hasContent ? "" : "collapse";
      host.style.display = hasContent ? "" : "none";
    };

    // Initial check
    updateVisibility();

    targets.forEach((target) => {
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
});
