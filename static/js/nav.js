const detectWrapping = (elements) => {
  let top = null;
  for (let i = 0; i < elements.length; i++) {
    let t = elements[i].getBoundingClientRect().top;
    if (top != null) {
      if (t != top) {
        return true;
      }
    }
    top = t;
  }
  return false;
};
window.addEventListener("load", function () {
  const elements = document.querySelectorAll("[nav-wrap]");
  const horisontal = document.querySelector("#nav-horisontal");
  const vertical = document.querySelector("#nav-vertical");
  const verticalContainer = document.querySelector("#vertical-nav-container");
  const toggle = document.querySelector("#nav-toggle");
  const padder = document.querySelector("#padder");
  toggle.addEventListener("click", (e) => {
    e.stopPropagation();
    verticalContainer.classList.toggle("hidden");
  });
  document.addEventListener("click", () => {
    verticalContainer.classList.add("hidden");
  });
  let wrap = true;
  let curentNav = vertical;
  const adjustVericalNavHeight = () => {
    const pad = curentNav.getBoundingClientRect().height;
    const scroll = document.documentElement.scrollHeight - pad;
    verticalContainer.style.height = scroll + "px";
    verticalContainer.style.paddingTop = pad + "px";
    padder.style.height = pad + "px";
  };
  const updateNav = () => {
    let w = detectWrapping(elements);
    if (w != wrap) {
      if (wrap) {
        horisontal.classList.remove("nav-hidden");
        vertical.classList.add("nav-hidden");
        verticalContainer.classList.add("nav-hidden");
        curentNav = horisontal;
      } else {
        horisontal.classList.add("nav-hidden");
        vertical.classList.remove("nav-hidden");
        verticalContainer.classList.remove("nav-hidden");
        curentNav = vertical;
      }
    }
    wrap = w;
    adjustVericalNavHeight();
  };
  window.addEventListener("resize", updateNav);
  updateNav();
});
