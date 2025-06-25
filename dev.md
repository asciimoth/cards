# Dev env setup
Dev environment is defined in `flake.nix`.

То setup dev environment
- Use `nix develop` (or `direnv allow` for direnv users) to enter env defined in `flake.nix`.
- Copy `.env.example` file to `.env` and fill placeholder fields with actual secrets.
- Run `go get` to fetch go dependencies

# Running service
```sh
docker compose up --build
```

# TODO
## Fronend
- [ ] Place name of logged in user somewhere
- [ ] Nav
  - [ ] Same paddings for buttons in side nav
  - [ ] Same buttons length in side nav
  - [ ] Move buttons under the burger in side nav
  - [ ] Remove main page button
  - [ ] Use SVG logo instead of just text
- [ ] Style cards page
  - [ ] If there is no cards, add text block about it
  - [ ] Add cards grid
  - [ ] Replace "create new card" link button with `+` sign
- [ ] Style editor page
  - [ ] Add live preview
  - [ ] Make crop widget not so fucking big
  - [ ] Style inputs (file inputs including)
- [ ] Style card page
- [ ] Style card not found page
- [ ] Style login page
  - [ ] Block with list of "sign with" buttons
  - [ ] Each button themed in related service colors and with logo
- [ ] Implement VCF file generation
- [ ] QR codes
  - [ ] Implement QR code with user provided logo generation
  - [ ] Generate two QR codes: one with link to card page and one with full VCF file (for offline usage)
- [ ] Favicon
  - [ ] Add default one
  - [ ] For cards use avatar as favicon
- [ ] PWA
- [ ] Add footer
## Backend
- [ ] Inmemory caching
  - [ ] Cards
  - [ ] S3 blobs
- [ ] Add TTL & etags for static files
- [ ] Statistic
  - [ ] Collect statistic inmemory and sync it to db periodically
- [ ] Localisation
- [ ] Add localisation system
- [ ] Add VIP accounts system
- [ ] Payment systems integration
- [ ] Paid features
  - [ ] Custom names
  - [ ] More cards
  - [ ] More fancy QR codes
  - [ ] Offline mode
## Content
- [ ] Fill main page with something
  - Maybe gallery of published cards
- [ ] FAQ
- [ ] Tutorials
## DevOps
- [ ] Add Heroku publishing helpers
- [ ] Add GH actions bases on nix
## Pr
- [ ] Add video demostration to readme
