export function formatDate(date) {
    const year = date.getFullYear();
    const month = String(date.getMonth() + 1).padStart(2, '0');
    const day = String(date.getDate()).padStart(2, '0');
    return `${year}-${month}-${day}`;
}

export function parseQuery(queryString) {
    const params = {};
    queryString.replace(/[?&]+([^=&]+)=([^&]*)/gi, (_, key, value) => {
        params[key] = decodeURIComponent(value);
    });
    return params;
}

export function slugify(text) {
    return text
        .toLowerCase()
        .replace(/\s+/g, '-')
        .replace(/[^\w-]+/g, '');
}
