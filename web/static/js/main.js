import { guard } from "./auth.js"
import renderErrorPage from "./components/error-page.js"
import { createRouter } from "./lib/router.js"

const modulesCache = new Map()
const viewsCache = new Map()
const disconnectEvent = new CustomEvent("disconnect")
const r = createRouter()
r.route("/", guard(view("home"), view("access")))
r.route(/^\/users\/(?<username>[a-zA-Z][a-zA-Z0-9_-]{0,17})$/, view("user"))
r.route(/^\/posts\/(?<postID>\d+)$/, view("post"))
r.route(/\//, view("not-found"))
r.subscribe(renderInto(document.querySelector("main")))
r.install()

function view(name) {
    return (...args) => {
        if (viewsCache.has(name)) {
            const renderPage = viewsCache.get(name)
            return renderPage(...args)
        }
        return import(`/js/components/${name}-page.js`).then(m => {
            const renderPage = m.default
            viewsCache.set(name, renderPage)
            return renderPage(...args)
        })
    }
}

async function importWithCache(identifier) {
    if (modulesCache.has(identifier)) {
        return modulesCache.get(identifier)
    }
    const m = await importModule(identifier)
    modulesCache.set(identifier, m)
    return m
}

/**
 * @param {Element} target
 */
function renderInto(target) {
    let currentPage = /** @type {Node=} */ (null)
    return async result => {
        if (currentPage instanceof Node) {
            currentPage.dispatchEvent(disconnectEvent)
            target.innerHTML = ""
        }
        try {
            currentPage = await result
        } catch (err) {
            console.error(err)
            currentPage = renderErrorPage(err)
        }
        target.appendChild(currentPage)
        setTimeout(activateLinks)
    }
}

function activateLinks() {
    const { pathname } = location
    const links = Array.from(document.querySelectorAll("a"))
    for (const link of links) {
        if (link.pathname === pathname) {
            link.setAttribute("aria-current", "page")
        } else {
            link.removeAttribute("aria-current")
        }
    }
}
