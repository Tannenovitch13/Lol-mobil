# Relais LolMobil (accès à distance)

Le relais permet au téléphone de joindre le PC **depuis n'importe où** (4G, chez un ami…), comme le
faisait *Rift* pour Mimic. C'est un petit programme pour **Cloudflare Workers**, qui tourne sur l'offre
**gratuite** : tu l'héberges toi-même sur ton compte Cloudflare, sans serveur à gérer.

- Le compagnon PC se connecte au relais, et le téléphone aussi. Le relais se contente de transmettre les
  messages entre les deux.
- **Tout est chiffré de bout en bout** (AES-256-GCM) avec une clé que seuls le PC et tes téléphones
  connaissent. Elle se trouve dans le lien d'accès à distance (après le `#`, une partie qu'un navigateur
  n'envoie jamais au serveur). Le relais, et donc Cloudflare, ne voit qu'un identifiant de salle et des
  données illisibles.
- Le relais sert aussi l'interface mobile en **HTTPS**.

## Déployer

### Option 1 : bouton (le plus simple)

[![Deploy to Cloudflare](https://deploy.workers.cloudflare.com/button)](https://deploy.workers.cloudflare.com/?url=https://github.com/Tannenovitch13/Lol-mobil)

Connecte-toi (ou crée un compte Cloudflare gratuit) et valide. Cloudflare copie le projet sur ton GitHub
puis déploie le Worker `lolmobil-relay`. À la fin, note l'adresse affichée, par exemple
`https://lolmobil-relay.<ton-nom>.workers.dev`.

### Option 2 : en ligne de commande

Il faut Node.js 18 ou plus récent :

```bash
git clone https://github.com/Tannenovitch13/Lol-mobil.git
cd Lol-mobil
npx wrangler login     # ouvre le navigateur pour te connecter à Cloudflare
npx wrangler deploy    # déploie le relais, affiche son adresse
```

## Brancher le compagnon

Lance le compagnon une fois avec l'adresse du relais (elle est ensuite retenue) :

```
LolMobil-Companion.exe -relay https://lolmobil-relay.<ton-nom>.workers.dev
```

La fenêtre du compagnon et la page `http://127.0.0.1:8787/pair` affichent maintenant un **second QR code
« Accès à distance »**. Scanne-le une fois avec l'appli Android ou avec l'appareil photo du téléphone :
la page s'ouvre en HTTPS et retient la clé. Ensuite, LolMobil fonctionne même hors de chez toi.

- **Garde ce lien secret** : il permet de contrôler ton client LoL.
- Lien compromis : `LolMobil-Companion.exe -reset-pairing` crée une nouvelle clé. Il faut alors
  rescanner le QR code sur chaque téléphone.
- Désactiver l'accès à distance : `-relay off`.

## Limites de l'offre gratuite

Un PC et quelques téléphones restent très loin des quotas gratuits de Cloudflare (requêtes Workers et
Durable Objects). En mode distant, les images du jeu viennent de
[Community Dragon](https://communitydragon.org) et non du PC, pour économiser la connexion.

## Développement

```bash
npx wrangler dev --port 8799          # relais local sur http://127.0.0.1:8799
go run ./cmd/lolmobil-companion -relay http://127.0.0.1:8799 ...
test/e2e/run-remote.sh                # test de bout en bout (mock client + relais local)
```

Le protocole est décrit en tête de `relay/src/index.ts`. Le chiffrement est décrit dans
`internal/relay/crypto.go` : le même schéma est implémenté dans `web/js/remote.js` et dans l'appli
Android (`Remote.kt`).
