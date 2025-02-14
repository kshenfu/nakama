import { isAuthenticated } from "./auth.js"
import { isPlainObject } from "./utils.js"

/**
 * @param {string} url
 * @param {{[key:string]:string}=} headers
 */
export function doGet(url, headers) {
    return fetch(url, {
        headers: Object.assign(defaultHeaders(), headers),
    }).then(parseResponse)
}

/**
 * @param {string} url
 * @param {{[field:string]:any}=} body
 * @param {{[key:string]:string}=} headers
 */
export function doPost(url, body, headers) {
    const init = {
        method: "POST",
        headers: defaultHeaders(),
    }
    if (isPlainObject(body)) {
        init["body"] = JSON.stringify(body)
        init.headers["content-type"] = "application/json; charset=utf-8"
    }
    Object.assign(init.headers, headers)
    return fetch(url, init).then(parseResponse)
}

/**
 * @param {string} url
 * @param {function} cb
 */
export function subscribe(url, cb) {
    if (isAuthenticated()) {
        const _url = new URL(url, location.origin)
        _url.searchParams.set("auth_token", localStorage.getItem("auth_token"))
        url = _url.toString()
    }
    const eventSource = new EventSource(url)
    eventSource.onmessage = ev => {
        try {
            cb(JSON.parse(ev.data))
        } catch (_) { }
    }
    return () => {
        eventSource.close()
    }
}

function defaultHeaders() {
    return isAuthenticated() ? {
        authorization: "Bearer " + localStorage.getItem("auth_token"),
    } : {}
}

/**
 * @param {Response} res
 * @returns {Promise<any>}
 */
async function parseResponse(res) {
    let body = await res.clone().json().catch(() => res.text())
    if (!res.ok) {
        const msg = String(body).trim().toLowerCase()
        const err = new Error(msg)
        err.name = msg
            .split(" ")
            .map(word => word.charAt(0).toUpperCase() + word.slice(1))
            .join("")
            + "Error"
        err["statusCode"] = res.status
        err["statusText"] = res.statusText
        err["url"] = res.url
        throw err
    }
    return body
}

export default {
    get: doGet,
    post: doPost,
    subscribe,
}
