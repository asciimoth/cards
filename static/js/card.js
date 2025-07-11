const QR_BASE_SIZE = 1000;
const OVERLAY_PADDING = 8;

const getId = (id) => {
  return document.getElementById(id);
};

const setupToggle = () => {
  document.querySelectorAll("[toggle-visibility]").forEach((host) => {
    console.log(host);
    const selector = host.getAttribute("toggle-visibility");
    const targets = document.querySelectorAll(selector);
    console.log(targets);
    host.addEventListener("click", () => {
      targets.forEach((target) => {
        console.log(target);
        if (target.style.visibility != "visible") {
          target.style.visibility = "visible";
          target.style.display = "block";
        } else {
          target.style.visibility = "collapse";
          target.style.display = "hidden";
        }
      });
    });
  });
};

const isValidImageURL = (url) => {
  if (!url) return false;
  if (url === window.location.href) return false;
  if (url.endsWith("/")) return false;
  return true;
};

const getCardData = () => {
  const name = (getId("card-name").textContent || "").trim();
  const position = (getId("card-position").textContent || "").trim();
  const company = (getId("card-company").textContent || "").trim();
  const phone = (getId("phone-span").textContent || "").trim();
  const email = (getId("email-span").textContent || "").trim();
  const description = (getId("card-description").textContent || "").trim();
  const logoImg = getId("card-logo");
  const avatarImg = getId("card-image-preview");

  let logo = "";
  if (logoImg && logoImg.src && isValidImageURL(logoImg.src)) {
    logo = logoImg.src;
  }

  let avatar = "";
  if (avatarImg && avatarImg.src && isValidImageURL(avatarImg.src)) {
    avatar = avatarImg.src;
  }

  return {
    name,
    position,
    company,
    phone,
    email,
    description,
    logo,
    avatar,
  };
};

const getVcf = () => {
  const cardData = getCardData();
  let vcard = "BEGIN:VCARD\nVERSION:3.0\nFN:" + cardData.name + "\n";
  if (cardData.phone) {
    vcard += "TEL:" + cardData.phone + "\n";
  }
  if (cardData.email) {
    vcard += "EMAIL:" + cardData.email + "\n";
  }
  vcard += "END:VCARD";
  return vcard;
};

const getOverlayImageInfo = () => {
  try {
    const cardData = getCardData();

    if (cardData.logo) {
      console.log("Using logo for QR overlay");
      return {
        url: cardData.logo,
        type: "logo",
        width: 400,
        height: 200,
      };
    }

    if (cardData.avatar) {
      console.log("Using avatar for QR overlay");
      return {
        url: cardData.avatar,
        type: "avatar",
        width: 300,
        height: 300,
      };
    }

    console.log("Using default image for QR overlay");
    return {
      url: "/static/favicon-192.svg",
      type: "default",
      width: 120,
      height: 120,
    };
  } catch (e) {
    console.error("Error getting overlay image:", e);
    return {
      url: "/static/favicon-192.svg",
      type: "default",
      width: 120,
      height: 120,
    };
  }
};

async function updateQRCode(text, targetId) {
  return new Promise((resolve) => {
    try {
      const imgInfo = getOverlayImageInfo();
      const img = new Image();
      img.crossOrigin = "Anonymous";
      img.src = imgInfo.url;

      img.onload = function () {
        const tempDiv = document.createElement("div");
        tempDiv.style.position = "absolute";
        tempDiv.style.left = "-9999px";
        document.body.appendChild(tempDiv);

        const qrcode = new QRCode(tempDiv, {
          text: text,
          width: QR_BASE_SIZE,
          height: QR_BASE_SIZE,
          colorDark: "#000000",
          colorLight: "#ffffff",
          correctLevel: QRCode.CorrectLevel.H,
        });

        setTimeout(() => {
          try {
            const qrCanvas = tempDiv.querySelector("canvas");
            if (!qrCanvas) {
              console.error("QR canvas not created!");
              resolve();
              return;
            }

            const combinedCanvas = document.createElement("canvas");
            combinedCanvas.width = QR_BASE_SIZE;
            combinedCanvas.height = QR_BASE_SIZE;
            const ctx = combinedCanvas.getContext("2d");

            ctx.drawImage(qrCanvas, 0, 0, QR_BASE_SIZE, QR_BASE_SIZE);

            let drawWidth = imgInfo.width;
            let drawHeight = imgInfo.height;
            const ratio = img.naturalWidth / img.naturalHeight;

            if (imgInfo.type === "logo") {
              if (ratio > 1) {
                drawHeight = drawWidth / ratio;
              } else {
                drawWidth = drawHeight * ratio;
              }
            }

            ctx.fillStyle = "#ffffff";
            ctx.fillRect(
              (QR_BASE_SIZE - drawWidth) / 2 - OVERLAY_PADDING,
              (QR_BASE_SIZE - drawHeight) / 2 - OVERLAY_PADDING,
              drawWidth + OVERLAY_PADDING * 2,
              drawHeight + OVERLAY_PADDING * 2,
            );

            ctx.drawImage(
              img,
              (QR_BASE_SIZE - drawWidth) / 2,
              (QR_BASE_SIZE - drawHeight) / 2,
              drawWidth,
              drawHeight,
            );

            const dataUrl = combinedCanvas.toDataURL("image/png");

            const targetImg = document.getElementById(targetId);
            if (targetImg) {
              targetImg.src = dataUrl;
              console.log(`QR image generated for ${targetId}`);
            } else {
              console.error(`Target image element not found: ${targetId}`);
            }

            document.body.removeChild(tempDiv);
            resolve();
          } catch (innerError) {
            console.error("Error in QR generation timeout:", innerError);
            resolve();
          }
        }, 100);
      };

      img.onerror = function () {
        console.error("Error loading overlay image:", imgInfo.url);
        resolve();
      };
    } catch (e) {
      console.error("Error in generateQRImage:", e);
      resolve();
    }
  });
}

async function updateQRCodes() {
  console.log("Generating QR codes...");
  await updateQRCode(window.location.href, "qr-code");
  await updateQRCode(getVcf(), "qr-code-offline");
  console.log("QR codes generated");
}

document.addEventListener("DOMContentLoaded", () => {
  setupToggle();
  getId("add-to-contacts-btn").addEventListener("click", () => {
    const blob = new Blob([getVcf()], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = getCardData().name + ".vcf";
    a.click();
    URL.revokeObjectURL(url);
  });
});
