document.addEventListener("DOMContentLoaded", () => {
  const previews = document.querySelectorAll("[clear-inputs]");
  previews.forEach((host) => {
    const selector = host.getAttribute("clear-inputs");
    const targets = document.querySelectorAll(selector);
    console.log(targets);
    host.addEventListener("click", () => {
      const event = new Event("change", {
        bubbles: true,
        cancelable: true,
      });
      targets.forEach((target) => {
        target.value = "";
        target.dispatchEvent(event);
      });
    });
  });
});
